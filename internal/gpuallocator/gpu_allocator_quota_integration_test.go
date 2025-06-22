package gpuallocator

import (
	"context"
	"fmt"
	"sync"
	"testing"

	tfv1 "github.com/NexusGPU/tensor-fusion/api/v1"
	"github.com/NexusGPU/tensor-fusion/internal/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGPUAllocator_QuotaIntegration(t *testing.T) {
	tests := []struct {
		name           string
		quota          *tfv1.GPUResourceQuota
		gpus           []tfv1.GPU
		allocRequests  []AllocRequest
		expectSuccess  []bool
		expectedUsage  *tfv1.GPUResourceUsage
		expectedErrors []string
	}{
		{
			name:  "successful allocation within quota",
			quota: createTestQuota("test-ns", 100, 1000, 10),
			gpus: []tfv1.GPU{
				createAvailableGPU("gpu1", "test-pool", 50, 500),
				createAvailableGPU("gpu2", "test-pool", 50, 500),
			},
			allocRequests: []AllocRequest{
				createAllocRequest(30, 300, 2),
			},
			expectSuccess: []bool{true},
			expectedUsage: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(60, resource.DecimalSI), // 30*2
				RequestsVRAM:   resource.NewQuantity(600, resource.BinarySI), // 300*2
				LimitsTFlops:   resource.NewQuantity(60, resource.DecimalSI), // 30*2
				LimitsVRAM:     resource.NewQuantity(600, resource.BinarySI), // 300*2
				Workers:        func() *int32 { i := int32(2); return &i }(), // 2 GPUs
			},
		},
		{
			name:  "allocation exceeds quota",
			quota: createTestQuota("test-ns", 100, 1000, 10),
			gpus: []tfv1.GPU{
				createAvailableGPU("gpu1", "test-pool", 100, 1000),
				createAvailableGPU("gpu2", "test-pool", 100, 1000),
			},
			allocRequests: []AllocRequest{
				createAllocRequest(60, 600, 2), // 60*2=120 > 100 quota
			},
			expectSuccess:  []bool{false},
			expectedErrors: []string{"total.requests.tflops"},
		},
		{
			name:  "multiple allocations within quota",
			quota: createTestQuota("test-ns", 100, 1000, 10),
			gpus: []tfv1.GPU{
				createAvailableGPU("gpu1", "test-pool", 50, 500),
				createAvailableGPU("gpu2", "test-pool", 50, 500),
				createAvailableGPU("gpu3", "test-pool", 50, 500),
				createAvailableGPU("gpu4", "test-pool", 50, 500),
			},
			allocRequests: []AllocRequest{
				createAllocRequest(20, 200, 2), // First allocation
				createAllocRequest(30, 300, 2), // Second allocation
			},
			expectSuccess: []bool{true, true},
			expectedUsage: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(100, resource.DecimalSI), // 40+60
				RequestsVRAM:   resource.NewQuantity(1000, resource.BinarySI), // 400+600
				LimitsTFlops:   resource.NewQuantity(100, resource.DecimalSI), // 40+60
				LimitsVRAM:     resource.NewQuantity(1000, resource.BinarySI), // 400+600
				Workers:        func() *int32 { i := int32(4); return &i }(),  // 2+2 GPUs
			},
		},
		{
			name:  "second allocation exceeds quota",
			quota: createTestQuota("test-ns", 100, 1000, 10),
			gpus: []tfv1.GPU{
				createAvailableGPU("gpu1", "test-pool", 50, 500),
				createAvailableGPU("gpu2", "test-pool", 50, 500),
				createAvailableGPU("gpu3", "test-pool", 50, 500),
				createAvailableGPU("gpu4", "test-pool", 50, 500),
			},
			allocRequests: []AllocRequest{
				createAllocRequest(40, 400, 2), // First allocation: 80 TFlops
				createAllocRequest(30, 300, 1), // Second allocation: would exceed (80+30=110 > 100)
			},
			expectSuccess:  []bool{true, false},
			expectedErrors: []string{"", "total.requests.tflops"},
			expectedUsage: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(80, resource.DecimalSI), // Only first allocation
				RequestsVRAM:   resource.NewQuantity(800, resource.BinarySI), // Only first allocation
				LimitsTFlops:   resource.NewQuantity(80, resource.DecimalSI), // Only first allocation
				LimitsVRAM:     resource.NewQuantity(800, resource.BinarySI), // Only first allocation
				Workers:        func() *int32 { i := int32(2); return &i }(), // Only first allocation
			},
		},
		{
			name:  "allocation with single limits - exceeds max per workload",
			quota: createTestQuotaWithSingleLimits(40, 400),
			gpus: []tfv1.GPU{
				createAvailableGPU("gpu1", "test-pool", 50, 500),
				createAvailableGPU("gpu2", "test-pool", 50, 500),
			},
			allocRequests: []AllocRequest{
				createAllocRequest(45, 350, 1), // 45 > 40 (single max TFlops)
			},
			expectSuccess:  []bool{false},
			expectedErrors: []string{"single.max.tflops"},
		},
		{
			name:  "allocation with single limits - below min per workload",
			quota: createTestQuotaWithSingleLimits(40, 400),
			gpus: []tfv1.GPU{
				createAvailableGPU("gpu1", "test-pool", 50, 500),
			},
			allocRequests: []AllocRequest{
				createAllocRequest(5, 50, 1), // 5 < 10 (single min TFlops)
			},
			expectSuccess:  []bool{false},
			expectedErrors: []string{"single.min.tflops"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			scheme := runtime.NewScheme()
			require.NoError(t, tfv1.AddToScheme(scheme))

			quotas := []tfv1.GPUResourceQuota{}
			if tt.quota != nil {
				quotas = append(quotas, *tt.quota)
			}

			// Add test GPU pool
			testPool := createTestGPUPool("test-pool")
			allObjects := objectsFromGPUs(tt.gpus)
			allObjects = append(allObjects, testPool)

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(allObjects...).
				WithLists(&tfv1.GPUResourceQuotaList{Items: quotas}).
				Build()

			ctx := context.Background()
			allocator := NewGpuAllocator(ctx, client, 0) // No sync interval for test

			// Initialize stores
			err := allocator.initGPUAndQuotaStore(ctx)
			require.NoError(t, err)

			// Perform allocations
			allocResults := make([][]*tfv1.GPU, len(tt.allocRequests))
			allocErrors := make([]error, len(tt.allocRequests))

			for i, req := range tt.allocRequests {
				allocResults[i], allocErrors[i] = allocator.Alloc(ctx, req)

				if tt.expectSuccess[i] {
					assert.NoError(t, allocErrors[i], "Allocation %d should succeed", i)
					assert.NotEmpty(t, allocResults[i], "Should return allocated GPUs")
				} else {
					assert.Error(t, allocErrors[i], "Allocation %d should fail", i)
					if len(tt.expectedErrors) > i && tt.expectedErrors[i] != "" {
						assert.Contains(t, allocErrors[i].Error(), tt.expectedErrors[i])
					}
				}
			}

			// Verify final quota usage
			if tt.expectedUsage != nil {
				usage, _, exists := allocator.quotaStore.GetQuotaStatus("test-ns")
				require.True(t, exists, "Quota should exist for test-ns")

				assert.True(t, tt.expectedUsage.RequestsTFlops.Equal(*usage.RequestsTFlops),
					"Expected TFlops: %s, Got: %s", tt.expectedUsage.RequestsTFlops.String(), usage.RequestsTFlops.String())
				assert.True(t, tt.expectedUsage.RequestsVRAM.Equal(*usage.RequestsVRAM),
					"Expected VRAM: %s, Got: %s", tt.expectedUsage.RequestsVRAM.String(), usage.RequestsVRAM.String())
				assert.Equal(t, *tt.expectedUsage.Workers, *usage.Workers,
					"Expected Workers: %d, Got: %d", *tt.expectedUsage.Workers, *usage.Workers)
			}
		})
	}
}

func TestGPUAllocator_QuotaDeallocation(t *testing.T) {
	// Setup
	scheme := runtime.NewScheme()
	require.NoError(t, tfv1.AddToScheme(scheme))

	quota := createTestQuota("test-ns", 100, 1000, 10)
	gpus := []tfv1.GPU{
		createAvailableGPU("gpu1", "test-pool", 50, 500),
		createAvailableGPU("gpu2", "test-pool", 50, 500),
	}

	// Add test GPU pool
	testPool := createTestGPUPool("test-pool")
	allObjects := objectsFromGPUs(gpus)
	allObjects = append(allObjects, testPool)

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(allObjects...).
		WithLists(&tfv1.GPUResourceQuotaList{Items: []tfv1.GPUResourceQuota{*quota}}).
		Build()

	ctx := context.Background()
	allocator := NewGpuAllocator(ctx, client, 0)

	// Initialize stores
	err := allocator.initGPUAndQuotaStore(ctx)
	require.NoError(t, err)

	// Allocate GPUs
	req := createAllocRequest(30, 300, 2)
	allocatedGPUs, err := allocator.Alloc(ctx, req)
	require.NoError(t, err)
	require.Len(t, allocatedGPUs, 2)

	// Verify allocation
	usage, available, exists := allocator.quotaStore.GetQuotaStatus("test-ns")
	require.True(t, exists)
	assert.Equal(t, int64(60), usage.RequestsTFlops.Value()) // 30*2
	assert.Equal(t, int64(600), usage.RequestsVRAM.Value())  // 300*2
	assert.Equal(t, int32(2), *usage.Workers)
	assert.Equal(t, int64(40), available.RequestsTFlops.Value()) // 100-60
	assert.Equal(t, int64(400), available.RequestsVRAM.Value())  // 1000-600
	assert.Equal(t, int32(8), *available.Workers)                // 10-2

	// Deallocate GPUs
	gpuNames := make([]types.NamespacedName, len(allocatedGPUs))
	for i, gpu := range allocatedGPUs {
		gpuNames[i] = types.NamespacedName{Name: gpu.Name}
	}

	allocator.Dealloc(ctx, req.WorkloadNameNamespace, req.Request, gpuNames)

	// Verify deallocation
	usage, available, exists = allocator.quotaStore.GetQuotaStatus("test-ns")
	require.True(t, exists)
	assert.Equal(t, int64(0), usage.RequestsTFlops.Value())       // Should be 0
	assert.Equal(t, int64(0), usage.RequestsVRAM.Value())         // Should be 0
	assert.Equal(t, int32(0), *usage.Workers)                     // Should be 0
	assert.Equal(t, int64(100), available.RequestsTFlops.Value()) // Back to full quota
	assert.Equal(t, int64(1000), available.RequestsVRAM.Value())  // Back to full quota
	assert.Equal(t, int32(10), *available.Workers)                // Back to full quota
}

func TestGPUAllocator_QuotaReconciliation(t *testing.T) {
	// Setup
	scheme := runtime.NewScheme()
	require.NoError(t, tfv1.AddToScheme(scheme))
	require.NoError(t, v1.AddToScheme(scheme))

	quota := createTestQuota("test-ns", 100, 1000, 10)
	gpus := []tfv1.GPU{
		createAvailableGPU("gpu1", "test-pool", 50, 500),
		createAvailableGPU("gpu2", "test-pool", 50, 500),
	}

	// Create worker pods that should be counted in quota usage
	workerPods := []v1.Pod{
		createWorkerPod("test-ns", "worker1", "20", "200"),
		createWorkerPod("test-ns", "worker2", "30", "300"),
	}

	// Add test GPU pool
	testPool := createTestGPUPool("test-pool")
	allObjects := objectsFromGPUs(gpus)
	allObjects = append(allObjects, testPool)

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(allObjects...).
		WithLists(
			&tfv1.GPUResourceQuotaList{Items: []tfv1.GPUResourceQuota{*quota}},
			&v1.PodList{Items: workerPods},
		).
		Build()

	ctx := context.Background()
	allocator := NewGpuAllocator(ctx, client, 0)

	// Initialize stores
	err := allocator.initGPUAndQuotaStore(ctx)
	require.NoError(t, err)

	// Manually trigger reconciliation (simulating leader change)
	allocator.reconcileAllocationState(ctx)

	// Verify quota usage reflects the worker pods
	usage, available, exists := allocator.quotaStore.GetQuotaStatus("test-ns")
	require.True(t, exists)

	assert.Equal(t, int64(50), usage.RequestsTFlops.Value())     // 20+30
	assert.Equal(t, int64(500), usage.RequestsVRAM.Value())      // 200+300
	assert.Equal(t, int32(2), *usage.Workers)                    // 2 pods
	assert.Equal(t, int64(50), available.RequestsTFlops.Value()) // 100-50
	assert.Equal(t, int64(500), available.RequestsVRAM.Value())  // 1000-500
	assert.Equal(t, int32(8), *available.Workers)                // 10-2
}

func TestGPUAllocator_NoQuotaDefined(t *testing.T) {
	// Setup without any quota
	scheme := runtime.NewScheme()
	require.NoError(t, tfv1.AddToScheme(scheme))

	gpus := []tfv1.GPU{
		createAvailableGPU("gpu1", "test-pool", 50, 500),
		createAvailableGPU("gpu2", "test-pool", 50, 500),
	}

	// Add test GPU pool
	testPool := createTestGPUPool("test-pool")
	allObjects := objectsFromGPUs(gpus)
	allObjects = append(allObjects, testPool)

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(allObjects...).
		WithLists(&tfv1.GPUResourceQuotaList{Items: []tfv1.GPUResourceQuota{}}). // Empty quota list
		Build()

	ctx := context.Background()
	allocator := NewGpuAllocator(ctx, client, 0)

	// Initialize stores
	err := allocator.initGPUAndQuotaStore(ctx)
	require.NoError(t, err)

	// Allocation should succeed without quota restrictions
	req := createAllocRequest(50, 500, 2)
	allocatedGPUs, err := allocator.Alloc(ctx, req)
	require.NoError(t, err)
	require.Len(t, allocatedGPUs, 2)

	// No quota status should be available
	_, _, exists := allocator.quotaStore.GetQuotaStatus("test-ns")
	assert.False(t, exists)
}

func TestQuotaStore_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		description string
		testFunc    func(t *testing.T)
	}{
		{
			name:        "nil resource quantities",
			description: "Test handling of nil resource quantities in quota spec",
			testFunc: func(t *testing.T) {
				qs := NewQuotaStore(nil)

				// Create quota with some nil quantities
				quota := &tfv1.GPUResourceQuota{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-quota",
						Namespace: "test-ns",
					},
					Spec: tfv1.GPUResourceQuotaSpec{
						Total: tfv1.GPUResourceQuotaTotal{
							RequestsTFlops: resource.NewQuantity(100, resource.DecimalSI),
							RequestsVRAM:   nil, // Nil VRAM quota
							LimitsTFlops:   nil, // Nil limits
							LimitsVRAM:     nil, // Nil limits
							Workers:        func() *int32 { i := int32(10); return &i }(),
						},
					},
				}

				entry := &QuotaStoreEntry{
					quota:        quota,
					currentUsage: createZeroUsage(),
					available:    createUsageFromQuota(quota),
				}

				qs.quotaStore["test-ns"] = entry

				// Should not panic and should handle nil quantities gracefully
				req := createAllocRequest(50, 500, 2)
				err := qs.checkQuotaAvailable("test-ns", req)
				assert.NoError(t, err) // Should pass since VRAM quota is nil (unlimited)

				qs.AllocateQuota("test-ns", req)

				// Verify allocation worked correctly
				assert.Equal(t, int64(100), entry.currentUsage.RequestsTFlops.Value()) // 50*2
				assert.Equal(t, int64(1000), entry.currentUsage.RequestsVRAM.Value())  // 500*2
				assert.Equal(t, int32(2), *entry.currentUsage.Workers)
			},
		},
		{
			name:        "zero quota values",
			description: "Test handling of zero quota values",
			testFunc: func(t *testing.T) {
				qs := NewQuotaStore(nil)

				quota := &tfv1.GPUResourceQuota{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "zero-quota",
						Namespace: "test-ns",
					},
					Spec: tfv1.GPUResourceQuotaSpec{
						Total: tfv1.GPUResourceQuotaTotal{
							RequestsTFlops: resource.NewQuantity(0, resource.DecimalSI),  // Zero quota
							RequestsVRAM:   resource.NewQuantity(0, resource.BinarySI),   // Zero quota
							LimitsTFlops:   resource.NewQuantity(0, resource.DecimalSI),  // Zero quota
							LimitsVRAM:     resource.NewQuantity(0, resource.BinarySI),   // Zero quota
							Workers:        func() *int32 { i := int32(0); return &i }(), // Zero workers
						},
					},
				}

				entry := &QuotaStoreEntry{
					quota:        quota,
					currentUsage: createZeroUsage(),
					available:    createUsageFromQuota(quota),
				}

				qs.quotaStore["test-ns"] = entry

				// Any allocation should fail with zero quota
				req := createAllocRequest(1, 1, 1)
				err := qs.checkQuotaAvailable("test-ns", req)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "total.requests.tflops")
			},
		},
		{
			name:        "concurrent allocation simulation",
			description: "Test quota allocation under simulated concurrent access",
			testFunc: func(t *testing.T) {
				qs := NewQuotaStore(nil)
				quota := createTestQuota("test-ns", 100, 1000, 10)

				entry := &QuotaStoreEntry{
					quota:        quota,
					currentUsage: createZeroUsage(),
					available:    createUsageFromQuota(quota),
				}

				qs.quotaStore["test-ns"] = entry

				// Simulate multiple allocations that together would exceed quota
				// In real concurrent scenario, only one should succeed due to locking
				req1 := createAllocRequest(10, 100, 6) // 60 TFlops total
				req2 := createAllocRequest(10, 100, 6) // 60 TFlops total, would exceed 100 TFlops limit

				// First allocation should succeed
				err1 := qs.checkQuotaAvailable("test-ns", req1)
				assert.NoError(t, err1)
				qs.AllocateQuota("test-ns", req1)

				// Second allocation should fail due to insufficient quota
				err2 := qs.checkQuotaAvailable("test-ns", req2)
				assert.Error(t, err2)
				assert.Contains(t, err2.Error(), "total.requests.tflops")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.testFunc(t)
		})
	}
}

// Helper functions

func createAvailableGPU(name, _ string, tflops, vram int64) tfv1.GPU {
	return tfv1.GPU{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				constants.LabelKeyOwner: "node1",     // Fixed node name for testing
				constants.GpuPoolKey:    "test-pool", // Always use test-pool
			},
		},
		Status: tfv1.GPUStatus{
			Phase: tfv1.TensorFusionGPUPhaseRunning,
			Capacity: &tfv1.Resource{
				Tflops: *resource.NewQuantity(tflops, resource.DecimalSI),
				Vram:   *resource.NewQuantity(vram, resource.BinarySI),
			},
			Available: &tfv1.Resource{
				Tflops: *resource.NewQuantity(tflops, resource.DecimalSI),
				Vram:   *resource.NewQuantity(vram, resource.BinarySI),
			},
			RunningApps: []*tfv1.RunningAppDetail{},
		},
	}
}

func objectsFromGPUs(gpus []tfv1.GPU) []client.Object {
	objects := make([]client.Object, len(gpus))
	for i := range gpus {
		objects[i] = &gpus[i]
	}
	return objects
}

//nolint:unparam // Test helper - name parameter is consistent by design
func createTestGPUPool(name string) *tfv1.GPUPool {
	return &tfv1.GPUPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: tfv1.GPUPoolSpec{
			// Basic spec fields as needed
		},
		Status: tfv1.GPUPoolStatus{
			Phase: tfv1.TensorFusionPoolPhaseRunning,
		},
	}
}

// High-Priority Integration Test Cases

func TestGPUAllocator_ConcurrentQuotaEnforcement(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, tfv1.AddToScheme(scheme))

	quota := createTestQuota("test-ns", 50, 500, 5) // Limited quota for testing
	gpus := []tfv1.GPU{
		createAvailableGPU("gpu1", "test-pool", 20, 200),
		createAvailableGPU("gpu2", "test-pool", 20, 200),
		createAvailableGPU("gpu3", "test-pool", 20, 200),
		createAvailableGPU("gpu4", "test-pool", 20, 200),
	}

	testPool := createTestGPUPool("test-pool")
	allObjects := objectsFromGPUs(gpus)
	allObjects = append(allObjects, testPool)

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(allObjects...).
		WithLists(&tfv1.GPUResourceQuotaList{Items: []tfv1.GPUResourceQuota{*quota}}).
		Build()

	ctx := context.Background()
	allocator := NewGpuAllocator(ctx, client, 0)

	// Initialize stores
	err := allocator.initGPUAndQuotaStore(ctx)
	require.NoError(t, err)

	var wg sync.WaitGroup
	results := make(chan error, 10)

	// Launch 6 concurrent allocation attempts, but quota only allows 5 workers
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_ = id                                // Unused in test goroutine
			req := createAllocRequest(10, 100, 1) // Each request uses 10 TFlops
			_, err := allocator.Alloc(ctx, req)
			results <- err
		}(i)
	}

	wg.Wait()
	close(results)

	// Count successes and failures
	successes := 0
	failures := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}

	// Should allow exactly 5 allocations (50 TFlops / 10 TFlops each)
	assert.Equal(t, 5, successes, "Should allow exactly 5 allocations")
	assert.Equal(t, 1, failures, "Should reject 1 allocation due to quota")

	// Verify final quota state
	usage, available, exists := allocator.quotaStore.GetQuotaStatus("test-ns")
	require.True(t, exists)
	assert.Equal(t, int64(50), usage.RequestsTFlops.Value(), "Should use full quota")
	assert.Equal(t, int64(0), available.RequestsTFlops.Value(), "No quota should remain")
	assert.Equal(t, int32(5), *usage.Workers, "Should have 5 workers allocated")
}

func TestGPUAllocator_QuotaBoundaryConditions(t *testing.T) {
	tests := []struct {
		name           string
		quotaTFlops    int64
		quotaVRAM      int64
		quotaWorkers   int32
		requestTFlops  int64
		requestVRAM    int64
		requestWorkers uint
		expectSuccess  bool
		expectedError  string
	}{
		{
			name:           "exact_quota_boundary",
			quotaTFlops:    100,
			quotaVRAM:      1000,
			quotaWorkers:   10,
			requestTFlops:  10,
			requestVRAM:    100,
			requestWorkers: 10, // Exactly matches quota
			expectSuccess:  true,
		},
		{
			name:           "exceed_quota_by_one_worker",
			quotaTFlops:    100,
			quotaVRAM:      1000,
			quotaWorkers:   10,
			requestTFlops:  9,
			requestVRAM:    90,
			requestWorkers: 11, // One more than quota allows
			expectSuccess:  false,
			expectedError:  "total.workers",
		},
		{
			name:           "zero_quota_allocation",
			quotaTFlops:    0,
			quotaVRAM:      0,
			quotaWorkers:   0,
			requestTFlops:  1,
			requestVRAM:    1,
			requestWorkers: 1,
			expectSuccess:  false,
			expectedError:  "total.requests.tflops",
		},
		{
			name:           "zero_request_allocation",
			quotaTFlops:    100,
			quotaVRAM:      1000,
			quotaWorkers:   10,
			requestTFlops:  0,
			requestVRAM:    0,
			requestWorkers: 1,
			expectSuccess:  true, // Zero resource requests should be allowed
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, tfv1.AddToScheme(scheme))

			quota := createTestQuota("test-ns", tt.quotaTFlops, tt.quotaVRAM, tt.quotaWorkers)

			// Create enough GPUs to satisfy the request if quota allows
			gpus := []tfv1.GPU{}
			for i := 0; i < int(tt.requestWorkers); i++ {
				gpu := createAvailableGPU(fmt.Sprintf("gpu%d", i+1), "test-pool",
					tt.requestTFlops+10, tt.requestVRAM+100) // GPUs have more than needed
				gpus = append(gpus, gpu)
			}

			testPool := createTestGPUPool("test-pool")
			allObjects := objectsFromGPUs(gpus)
			allObjects = append(allObjects, testPool)

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(allObjects...).
				WithLists(&tfv1.GPUResourceQuotaList{Items: []tfv1.GPUResourceQuota{*quota}}).
				Build()

			ctx := context.Background()
			allocator := NewGpuAllocator(ctx, client, 0)

			err := allocator.initGPUAndQuotaStore(ctx)
			require.NoError(t, err)

			req := createAllocRequest(tt.requestTFlops, tt.requestVRAM, tt.requestWorkers)
			allocatedGPUs, err := allocator.Alloc(ctx, req)

			if tt.expectSuccess {
				assert.NoError(t, err, "Allocation should succeed for test: %s", tt.name)
				assert.Len(t, allocatedGPUs, int(tt.requestWorkers), "Should allocate correct number of GPUs")
			} else {
				assert.Error(t, err, "Allocation should fail for test: %s", tt.name)
				if tt.expectedError != "" {
					assert.Contains(t, err.Error(), tt.expectedError, "Error should contain expected text")
				}
				assert.Empty(t, allocatedGPUs, "Should not allocate any GPUs on failure")
			}
		})
	}
}

func TestGPUAllocator_QuotaErrorRecovery(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, tfv1.AddToScheme(scheme))

	quota := createTestQuota("test-ns", 100, 1000, 10)
	gpus := []tfv1.GPU{
		createAvailableGPU("gpu1", "test-pool", 30, 300),
		createAvailableGPU("gpu2", "test-pool", 30, 300),
		createAvailableGPU("gpu3", "test-pool", 30, 300),
		createAvailableGPU("gpu4", "test-pool", 30, 300),
	}

	testPool := createTestGPUPool("test-pool")
	allObjects := objectsFromGPUs(gpus)
	allObjects = append(allObjects, testPool)

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(allObjects...).
		WithLists(&tfv1.GPUResourceQuotaList{Items: []tfv1.GPUResourceQuota{*quota}}).
		Build()

	ctx := context.Background()
	allocator := NewGpuAllocator(ctx, client, 0)

	err := allocator.initGPUAndQuotaStore(ctx)
	require.NoError(t, err)

	// Step 1: Successful allocation
	req1 := createAllocRequest(25, 250, 2)
	allocatedGPUs1, err := allocator.Alloc(ctx, req1)
	require.NoError(t, err, "First allocation should succeed")
	require.Len(t, allocatedGPUs1, 2, "Should allocate 2 GPUs")

	// Verify quota usage
	usage, available, exists := allocator.quotaStore.GetQuotaStatus("test-ns")
	require.True(t, exists)
	assert.Equal(t, int64(50), usage.RequestsTFlops.Value(), "Should use 50 TFlops") // 25*2
	assert.Equal(t, int64(50), available.RequestsTFlops.Value(), "Should have 50 TFlops available")

	// Step 2: Deallocate and verify recovery
	workload := tfv1.NameNamespace{Namespace: "test-ns", Name: "test-workload"}

	// Convert GPU objects to NamespacedName
	gpuNames := make([]types.NamespacedName, len(allocatedGPUs1))
	for i, gpu := range allocatedGPUs1 {
		gpuNames[i] = types.NamespacedName{
			Name:      gpu.Name,
			Namespace: gpu.Namespace,
		}
	}

	allocator.Dealloc(ctx, workload, req1.Request, gpuNames)

	// Verify quota is fully recovered
	usage, available, exists = allocator.quotaStore.GetQuotaStatus("test-ns")
	require.True(t, exists)
	assert.Equal(t, int64(0), usage.RequestsTFlops.Value(), "Usage should be reset to 0")
	assert.Equal(t, int64(100), available.RequestsTFlops.Value(), "Full quota should be available")
	assert.Equal(t, int32(0), *usage.Workers, "No workers should be in use")
	assert.Equal(t, int32(10), *available.Workers, "All workers should be available")

	// Step 3: Verify we can allocate again after recovery
	req2 := createAllocRequest(30, 300, 3)
	allocatedGPUs2, err := allocator.Alloc(ctx, req2)
	assert.NoError(t, err, "Should be able to allocate after recovery")
	assert.Len(t, allocatedGPUs2, 3, "Should allocate 3 GPUs after recovery")

	// Verify new allocation uses quota correctly
	usage, available, exists = allocator.quotaStore.GetQuotaStatus("test-ns")
	require.True(t, exists)
	assert.Equal(t, int64(90), usage.RequestsTFlops.Value(), "Should use 90 TFlops") // 30*3
	assert.Equal(t, int64(10), available.RequestsTFlops.Value(), "Should have 10 TFlops available")
}

func TestGPUAllocator_QuotaReconciliationRobustness(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, tfv1.AddToScheme(scheme))
	require.NoError(t, v1.AddToScheme(scheme))

	quota := createTestQuota("test-ns", 100, 1000, 10)
	gpus := []tfv1.GPU{
		createAvailableGPU("gpu1", "test-pool", 50, 500),
		createAvailableGPU("gpu2", "test-pool", 50, 500),
	}

	// Create worker pods that represent actual running workloads
	workerPods := []v1.Pod{
		createWorkerPod("test-ns", "worker1", "30", "300"),
		createWorkerPod("test-ns", "worker2", "20", "200"),
		createWorkerPod("other-ns", "worker3", "10", "100"), // Different namespace
	}

	testPool := createTestGPUPool("test-pool")
	allObjects := objectsFromGPUs(gpus)
	allObjects = append(allObjects, testPool)

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(allObjects...).
		WithLists(
			&tfv1.GPUResourceQuotaList{Items: []tfv1.GPUResourceQuota{*quota}},
			&v1.PodList{Items: workerPods},
		).
		Build()

	ctx := context.Background()
	allocator := NewGpuAllocator(ctx, client, 0)

	err := allocator.initGPUAndQuotaStore(ctx)
	require.NoError(t, err)

	// Manually trigger reconciliation (simulating system restart)
	allocator.reconcileAllocationState(ctx)

	// Verify quota usage reflects the worker pods (only test-ns pods should count)
	usage, available, exists := allocator.quotaStore.GetQuotaStatus("test-ns")
	require.True(t, exists)

	assert.Equal(t, int64(50), usage.RequestsTFlops.Value(), "Should reflect actual pod usage: 30+20")
	assert.Equal(t, int64(500), usage.RequestsVRAM.Value(), "Should reflect actual pod VRAM: 300+200")
	assert.Equal(t, int32(2), *usage.Workers, "Should count 2 worker pods in test-ns")

	assert.Equal(t, int64(50), available.RequestsTFlops.Value(), "Remaining quota: 100-50")
	assert.Equal(t, int64(500), available.RequestsVRAM.Value(), "Remaining VRAM: 1000-500")
	assert.Equal(t, int32(8), *available.Workers, "Remaining workers: 10-2")

	// Test that reconciliation handles deleted pods correctly
	deletedPod := createDeletedWorkerPod("test-ns", "worker-deleted", "15", "150")
	workerPodsWithDeleted := append(workerPods, deletedPod)

	// Update the client with deleted pod
	client = fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(allObjects...).
		WithLists(
			&tfv1.GPUResourceQuotaList{Items: []tfv1.GPUResourceQuota{*quota}},
			&v1.PodList{Items: workerPodsWithDeleted},
		).
		Build()

	allocator.Client = client
	allocator.reconcileAllocationState(ctx)

	// Verify deleted pod is ignored
	usage, _, exists = allocator.quotaStore.GetQuotaStatus("test-ns")
	require.True(t, exists)
	assert.Equal(t, int64(50), usage.RequestsTFlops.Value(), "Deleted pod should not affect usage")
	assert.Equal(t, int32(2), *usage.Workers, "Deleted pod should not be counted")
}

func TestGPUAllocator_LargeScaleQuotaOperations(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, tfv1.AddToScheme(scheme))

	// Create a large quota for stress testing
	quota := createTestQuota("test-ns", 1000, 10000, 100)

	// Create many GPUs
	gpus := []tfv1.GPU{}
	for i := 0; i < 50; i++ {
		gpu := createAvailableGPU(fmt.Sprintf("gpu%d", i+1), "test-pool", 25, 250)
		gpus = append(gpus, gpu)
	}

	testPool := createTestGPUPool("test-pool")
	allObjects := objectsFromGPUs(gpus)
	allObjects = append(allObjects, testPool)

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(allObjects...).
		WithLists(&tfv1.GPUResourceQuotaList{Items: []tfv1.GPUResourceQuota{*quota}}).
		Build()

	ctx := context.Background()
	allocator := NewGpuAllocator(ctx, client, 0)

	err := allocator.initGPUAndQuotaStore(ctx)
	require.NoError(t, err)

	// Perform many small allocations
	allocatedGPUs := make([][]*tfv1.GPU, 0)
	for i := 0; i < 40; i++ { // 40 allocations of 2 GPUs each = 80 workers (within 100 limit)
		req := createAllocRequest(20, 200, 2) // Each allocation uses 40 TFlops total
		gpus, err := allocator.Alloc(ctx, req)

		if err != nil {
			t.Logf("Allocation %d failed: %v", i, err)
			break
		}

		assert.Len(t, gpus, 2, "Each allocation should get 2 GPUs")
		allocatedGPUs = append(allocatedGPUs, gpus)
	}

	// Verify final quota state
	usage, available, exists := allocator.quotaStore.GetQuotaStatus("test-ns")
	require.True(t, exists)

	// Should have allocated close to the quota limit
	assert.GreaterOrEqual(t, usage.RequestsTFlops.Value(), int64(600), "Should use substantial quota")
	assert.LessOrEqual(t, usage.RequestsTFlops.Value(), int64(1000), "Should not exceed quota")
	assert.GreaterOrEqual(t, *usage.Workers, int32(30), "Should allocate substantial workers")
	assert.LessOrEqual(t, *usage.Workers, int32(100), "Should not exceed worker limit")

	// Verify quota consistency
	totalUsed := usage.RequestsTFlops.Value()
	totalAvail := available.RequestsTFlops.Value()
	assert.Equal(t, int64(1000), totalUsed+totalAvail, "Usage + Available should equal total quota")

	t.Logf("Successfully allocated %d GPU sets, using %d TFlops and %d workers",
		len(allocatedGPUs), usage.RequestsTFlops.Value(), *usage.Workers)
}
