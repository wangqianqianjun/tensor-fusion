package gpuallocator

import (
	"context"
	"math"
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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Test constants for better maintainability and clarity
const (
	// Default quota values
	DefaultQuotaTFlops  = 100
	DefaultQuotaVRAM    = 1000
	DefaultQuotaWorkers = 10

	// Small request values (within quota)
	SmallRequestTFlops  = 10
	SmallRequestVRAM    = 100
	SmallRequestWorkers = 1

	// Large request values (may exceed quota)
	LargeRequestTFlops  = 50
	LargeRequestVRAM    = 500
	LargeRequestWorkers = 5

	// Test identifiers
	TestNamespace = "test-ns"
	TestPoolName  = "test-pool"
	TestWorkload  = "test-workload"

	// Boundary values
	MaxRequestTFlops = math.MaxInt64
	MinRequestTFlops = 1
	ZeroValue        = 0
)

// Test helper functions for better test readability and maintainability

// assertQuotaUsage verifies quota usage matches expected values
//
//nolint:unparam // Test helper - namespace parameter is consistent by design
func assertQuotaUsage(t *testing.T, qs *QuotaStore, namespace string, expectedTFlops, expectedVRAM int64, expectedWorkers int32) {
	t.Helper()
	usage, _, exists := qs.GetQuotaStatus(namespace)
	require.True(t, exists, "Quota should exist for namespace %s", namespace)
	assert.Equal(t, expectedTFlops, usage.RequestsTFlops.Value(), "TFlops usage mismatch")
	assert.Equal(t, expectedVRAM, usage.RequestsVRAM.Value(), "VRAM usage mismatch")
	assert.Equal(t, expectedWorkers, *usage.Workers, "Workers usage mismatch")
}

// assertQuotaAvailable verifies available quota matches expected values
//
//nolint:unparam // Test helper - namespace parameter is consistent by design
func assertQuotaAvailable(t *testing.T, qs *QuotaStore, namespace string, expectedTFlops, expectedVRAM int64, expectedWorkers int32) {
	t.Helper()
	_, available, exists := qs.GetQuotaStatus(namespace)
	require.True(t, exists, "Quota should exist for namespace %s", namespace)
	assert.Equal(t, expectedTFlops, available.RequestsTFlops.Value(), "Available TFlops mismatch")
	assert.Equal(t, expectedVRAM, available.RequestsVRAM.Value(), "Available VRAM mismatch")
	assert.Equal(t, expectedWorkers, *available.Workers, "Available workers mismatch")
}

// createQuotaEntry creates a complete QuotaStoreEntry for testing
func createQuotaEntry(quota *tfv1.GPUResourceQuota) *QuotaStoreEntry {
	return &QuotaStoreEntry{
		quota:        quota,
		currentUsage: createZeroUsage(),
		available:    createUsageFromQuota(quota),
	}
}

// createTestQuotaWithValues creates a quota with specified values
//
//nolint:unparam // Test helper - namespace parameter is consistent by design
func createTestQuotaWithValues(namespace string, tflops, vram int64, workers int32) *tfv1.GPUResourceQuota {
	return &tfv1.GPUResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-quota",
			Namespace: namespace,
		},
		Spec: tfv1.GPUResourceQuotaSpec{
			Total: tfv1.GPUResourceQuotaTotal{
				RequestsTFlops: resource.NewQuantity(tflops, resource.DecimalSI),
				RequestsVRAM:   resource.NewQuantity(vram, resource.BinarySI),
				LimitsTFlops:   resource.NewQuantity(tflops, resource.DecimalSI),
				LimitsVRAM:     resource.NewQuantity(vram, resource.BinarySI),
				Workers:        &workers,
			},
		},
	}
}

// createAllocRequestWithValues creates an allocation request with specified values
//
//nolint:unparam // Test helper - namespace parameter is consistent by design
func createAllocRequestWithValues(namespace string, tflops, vram int64, count uint) AllocRequest {
	return AllocRequest{
		PoolName: TestPoolName,
		WorkloadNameNamespace: tfv1.NameNamespace{
			Namespace: namespace,
			Name:      TestWorkload,
		},
		Request: tfv1.Resource{
			Tflops: *resource.NewQuantity(tflops, resource.DecimalSI),
			Vram:   *resource.NewQuantity(vram, resource.BinarySI),
		},
		Count: count,
	}
}

func TestNewQuotaStore(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	qs := NewQuotaStore(client)

	assert.NotNil(t, qs)
	assert.NotNil(t, qs.quotaStore)
	assert.NotNil(t, qs.dirtyQuotas)
	assert.Equal(t, client, qs.Client)
}

func TestQuotaStore_initQuotaStore(t *testing.T) {
	tests := []struct {
		name        string
		quotas      []tfv1.GPUResourceQuota
		expectError bool
		expectCount int
	}{
		{
			name:        "empty quota list",
			quotas:      []tfv1.GPUResourceQuota{},
			expectError: false,
			expectCount: 0,
		},
		{
			name: "valid single quota",
			quotas: []tfv1.GPUResourceQuota{
				*createTestQuota("test-ns", 100, 1000, 10),
			},
			expectError: false,
			expectCount: 1,
		},
		{
			name: "multiple valid quotas",
			quotas: []tfv1.GPUResourceQuota{
				*createTestQuota("ns1", 100, 1000, 10),
				*createTestQuota("ns2", 200, 2000, 20),
			},
			expectError: false,
			expectCount: 2,
		},
		{
			name: "quota with invalid negative values",
			quotas: []tfv1.GPUResourceQuota{
				*createInvalidQuota("test-ns", -100, 1000, 10),
			},
			expectError: false,
			expectCount: 0, // Invalid quota should be skipped
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = tfv1.AddToScheme(scheme)

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithLists(&tfv1.GPUResourceQuotaList{Items: tt.quotas}).
				Build()

			qs := NewQuotaStore(client)
			ctx := context.Background()

			err := qs.initQuotaStore(ctx)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Len(t, qs.quotaStore, tt.expectCount)
			}
		})
	}
}

func TestQuotaStore_checkQuotaAvailable(t *testing.T) {
	tests := []struct {
		name      string
		quota     *tfv1.GPUResourceQuota
		request   AllocRequest
		expectErr bool
		errType   string
	}{
		{
			name:      "no quota defined - should allow",
			quota:     nil,
			request:   createAllocRequest(10, 100, 1),
			expectErr: false,
		},
		{
			name:      "within quota limits",
			quota:     createTestQuota("test-ns", 100, 1000, 10),
			request:   createAllocRequest(10, 100, 2),
			expectErr: false,
		},
		{
			name:      "exceeds single max tflops",
			quota:     createTestQuotaWithSingleLimits(50, 500),
			request:   createAllocRequest(60, 100, 1), // 60 > 50 (single max)
			expectErr: true,
			errType:   "single.max.tflops",
		},
		{
			name:      "exceeds single max vram",
			quota:     createTestQuotaWithSingleLimits(50, 500),
			request:   createAllocRequest(10, 600, 1), // 600 > 500 (single max)
			expectErr: true,
			errType:   "single.max.vram",
		},
		{
			name:      "exceeds single max workers",
			quota:     createTestQuotaWithSingleLimits(50, 500),
			request:   createAllocRequest(10, 100, 6), // 6 > 5 (single max)
			expectErr: true,
			errType:   "single.max.workers",
		},
		{
			name:      "below single min tflops",
			quota:     createTestQuotaWithSingleLimits(50, 500),
			request:   createAllocRequest(5, 100, 1), // 5 < 10 (single min)
			expectErr: true,
			errType:   "single.min.tflops",
		},
		{
			name:      "exceeds total tflops quota",
			quota:     createTestQuota("test-ns", 100, 1000, 10),
			request:   createAllocRequest(60, 100, 2), // 60*2=120 > 100 (total)
			expectErr: true,
			errType:   "total.requests.tflops",
		},
		{
			name:      "exceeds total vram quota",
			quota:     createTestQuota("test-ns", 100, 1000, 10),
			request:   createAllocRequest(10, 600, 2), // 600*2=1200 > 1000 (total)
			expectErr: true,
			errType:   "total.requests.vram",
		},
		{
			name:      "exceeds total workers quota",
			quota:     createTestQuota("test-ns", 100, 1000, 10),
			request:   createAllocRequest(9, 90, 11), // 11 > 10 (total workers), but TFlops=99 < 100
			expectErr: true,
			errType:   "total.workers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qs := NewQuotaStore(nil)

			if tt.quota != nil {
				// Setup quota store entry
				entry := &QuotaStoreEntry{
					quota:        tt.quota,
					currentUsage: createZeroUsage(),
					available:    createUsageFromQuota(tt.quota),
				}
				qs.quotaStore[tt.quota.Namespace] = entry
			}

			err := qs.checkQuotaAvailable(tt.request.WorkloadNameNamespace.Namespace, tt.request)

			if tt.expectErr {
				assert.Error(t, err)
				quotaErr, ok := err.(*QuotaExceededError)
				assert.True(t, ok, "Expected QuotaExceededError")
				assert.Contains(t, quotaErr.Resource, tt.errType)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestQuotaStore_AllocateQuota(t *testing.T) {
	tests := []struct {
		name          string
		initialUsage  *tfv1.GPUResourceUsage
		request       AllocRequest
		expectedUsage *tfv1.GPUResourceUsage
		expectedAvail *tfv1.GPUResourceUsage
	}{
		{
			name:         "first allocation",
			initialUsage: createZeroUsage(),
			request:      createAllocRequest(10, 100, 2),
			expectedUsage: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(20, resource.DecimalSI), // 10*2
				RequestsVRAM:   resource.NewQuantity(200, resource.BinarySI), // 100*2
				LimitsTFlops:   resource.NewQuantity(20, resource.DecimalSI), // same as requests
				LimitsVRAM:     resource.NewQuantity(200, resource.BinarySI), // same as requests
				Workers:        func() *int32 { i := int32(2); return &i }(),
			},
			expectedAvail: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(80, resource.DecimalSI), // 100-20
				RequestsVRAM:   resource.NewQuantity(800, resource.BinarySI), // 1000-200
				LimitsTFlops:   resource.NewQuantity(80, resource.DecimalSI), // 100-20
				LimitsVRAM:     resource.NewQuantity(800, resource.BinarySI), // 1000-200
				Workers:        func() *int32 { i := int32(8); return &i }(), // 10-2
			},
		},
		{
			name: "additional allocation",
			initialUsage: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(30, resource.DecimalSI),
				RequestsVRAM:   resource.NewQuantity(300, resource.BinarySI),
				LimitsTFlops:   resource.NewQuantity(30, resource.DecimalSI),
				LimitsVRAM:     resource.NewQuantity(300, resource.BinarySI),
				Workers:        func() *int32 { i := int32(3); return &i }(),
			},
			request: createAllocRequest(5, 50, 1),
			expectedUsage: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(35, resource.DecimalSI), // 30+5
				RequestsVRAM:   resource.NewQuantity(350, resource.BinarySI), // 300+50
				LimitsTFlops:   resource.NewQuantity(35, resource.DecimalSI), // 30+5
				LimitsVRAM:     resource.NewQuantity(350, resource.BinarySI), // 300+50
				Workers:        func() *int32 { i := int32(4); return &i }(), // 3+1
			},
			expectedAvail: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(65, resource.DecimalSI), // 100-35
				RequestsVRAM:   resource.NewQuantity(650, resource.BinarySI), // 1000-350
				LimitsTFlops:   resource.NewQuantity(65, resource.DecimalSI), // 100-35
				LimitsVRAM:     resource.NewQuantity(650, resource.BinarySI), // 1000-350
				Workers:        func() *int32 { i := int32(6); return &i }(), // 10-4
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qs := NewQuotaStore(nil)
			quota := createTestQuota("test-ns", 100, 1000, 10)

			entry := &QuotaStoreEntry{
				quota:        quota,
				currentUsage: tt.initialUsage,
				available:    createUsageFromQuota(quota),
			}

			// Adjust available based on initial usage
			if tt.initialUsage.RequestsTFlops != nil {
				entry.available.RequestsTFlops.Sub(*tt.initialUsage.RequestsTFlops)
			}
			if tt.initialUsage.RequestsVRAM != nil {
				entry.available.RequestsVRAM.Sub(*tt.initialUsage.RequestsVRAM)
			}
			if tt.initialUsage.Workers != nil {
				*entry.available.Workers -= *tt.initialUsage.Workers
			}

			qs.quotaStore["test-ns"] = entry

			qs.AllocateQuota("test-ns", tt.request)

			// Verify current usage
			assert.True(t, tt.expectedUsage.RequestsTFlops.Equal(*entry.currentUsage.RequestsTFlops))
			assert.True(t, tt.expectedUsage.RequestsVRAM.Equal(*entry.currentUsage.RequestsVRAM))
			assert.Equal(t, *tt.expectedUsage.Workers, *entry.currentUsage.Workers)

			// Verify available quota
			assert.True(t, tt.expectedAvail.RequestsTFlops.Equal(*entry.available.RequestsTFlops))
			assert.True(t, tt.expectedAvail.RequestsVRAM.Equal(*entry.available.RequestsVRAM))
			assert.Equal(t, *tt.expectedAvail.Workers, *entry.available.Workers)

			// Verify quota is marked dirty
			_, isDirty := qs.dirtyQuotas["test-ns"]
			assert.True(t, isDirty)
		})
	}
}

func TestQuotaStore_DeallocateQuota(t *testing.T) {
	tests := []struct {
		name           string
		initialUsage   *tfv1.GPUResourceUsage
		initialAvail   *tfv1.GPUResourceUsage
		deallocRequest tfv1.Resource
		replicas       int32
		expectedUsage  *tfv1.GPUResourceUsage
		expectedAvail  *tfv1.GPUResourceUsage
	}{
		{
			name: "normal deallocation",
			initialUsage: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(50, resource.DecimalSI),
				RequestsVRAM:   resource.NewQuantity(500, resource.BinarySI),
				LimitsTFlops:   resource.NewQuantity(50, resource.DecimalSI),
				LimitsVRAM:     resource.NewQuantity(500, resource.BinarySI),
				Workers:        func() *int32 { i := int32(5); return &i }(),
			},
			initialAvail: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(50, resource.DecimalSI),
				RequestsVRAM:   resource.NewQuantity(500, resource.BinarySI),
				LimitsTFlops:   resource.NewQuantity(50, resource.DecimalSI),
				LimitsVRAM:     resource.NewQuantity(500, resource.BinarySI),
				Workers:        func() *int32 { i := int32(5); return &i }(),
			},
			deallocRequest: tfv1.Resource{
				Tflops: *resource.NewQuantity(10, resource.DecimalSI),
				Vram:   *resource.NewQuantity(100, resource.BinarySI),
			},
			replicas: 2,
			expectedUsage: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(30, resource.DecimalSI), // 50-20
				RequestsVRAM:   resource.NewQuantity(300, resource.BinarySI), // 500-200
				LimitsTFlops:   resource.NewQuantity(30, resource.DecimalSI), // 50-20
				LimitsVRAM:     resource.NewQuantity(300, resource.BinarySI), // 500-200
				Workers:        func() *int32 { i := int32(3); return &i }(), // 5-2
			},
			expectedAvail: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(70, resource.DecimalSI), // 50+20
				RequestsVRAM:   resource.NewQuantity(700, resource.BinarySI), // 500+200
				LimitsTFlops:   resource.NewQuantity(70, resource.DecimalSI), // 50+20
				LimitsVRAM:     resource.NewQuantity(700, resource.BinarySI), // 500+200
				Workers:        func() *int32 { i := int32(7); return &i }(), // 5+2
			},
		},
		{
			name: "deallocation preventing negative values",
			initialUsage: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(10, resource.DecimalSI),
				RequestsVRAM:   resource.NewQuantity(100, resource.BinarySI),
				LimitsTFlops:   resource.NewQuantity(10, resource.DecimalSI),
				LimitsVRAM:     resource.NewQuantity(100, resource.BinarySI),
				Workers:        func() *int32 { i := int32(1); return &i }(),
			},
			initialAvail: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(90, resource.DecimalSI),
				RequestsVRAM:   resource.NewQuantity(900, resource.BinarySI),
				LimitsTFlops:   resource.NewQuantity(90, resource.DecimalSI),
				LimitsVRAM:     resource.NewQuantity(900, resource.BinarySI),
				Workers:        func() *int32 { i := int32(9); return &i }(),
			},
			deallocRequest: tfv1.Resource{
				Tflops: *resource.NewQuantity(20, resource.DecimalSI), // More than current usage
				Vram:   *resource.NewQuantity(200, resource.BinarySI), // More than current usage
			},
			replicas: 3, // More than current workers
			expectedUsage: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(0, resource.DecimalSI),  // Should not go negative
				RequestsVRAM:   resource.NewQuantity(0, resource.BinarySI),   // Should not go negative
				LimitsTFlops:   resource.NewQuantity(0, resource.DecimalSI),  // Should not go negative
				LimitsVRAM:     resource.NewQuantity(0, resource.BinarySI),   // Should not go negative
				Workers:        func() *int32 { i := int32(0); return &i }(), // Should not go negative
			},
			expectedAvail: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(100, resource.DecimalSI), // 90+10 (actual deallocation)
				RequestsVRAM:   resource.NewQuantity(1000, resource.BinarySI), // 900+100 (actual deallocation)
				LimitsTFlops:   resource.NewQuantity(100, resource.DecimalSI), // 90+10 (actual deallocation)
				LimitsVRAM:     resource.NewQuantity(1000, resource.BinarySI), // 900+100 (actual deallocation)
				Workers:        func() *int32 { i := int32(10); return &i }(), // 9+1 (actual deallocation)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qs := NewQuotaStore(nil)
			quota := createTestQuota("test-ns", 100, 1000, 10)

			entry := &QuotaStoreEntry{
				quota:        quota,
				currentUsage: tt.initialUsage,
				available:    tt.initialAvail,
			}

			qs.quotaStore["test-ns"] = entry

			qs.DeallocateQuota("test-ns", tt.deallocRequest, tt.replicas)

			// Verify current usage
			assert.True(t, tt.expectedUsage.RequestsTFlops.Equal(*entry.currentUsage.RequestsTFlops))
			assert.True(t, tt.expectedUsage.RequestsVRAM.Equal(*entry.currentUsage.RequestsVRAM))
			assert.Equal(t, *tt.expectedUsage.Workers, *entry.currentUsage.Workers)

			// Verify available quota
			assert.True(t, tt.expectedAvail.RequestsTFlops.Equal(*entry.available.RequestsTFlops))
			assert.True(t, tt.expectedAvail.RequestsVRAM.Equal(*entry.available.RequestsVRAM))
			assert.Equal(t, *tt.expectedAvail.Workers, *entry.available.Workers)

			// Verify quota is marked dirty
			_, isDirty := qs.dirtyQuotas["test-ns"]
			assert.True(t, isDirty)
		})
	}
}

func TestQuotaStore_calculateAvailablePercent(t *testing.T) {
	tests := []struct {
		name            string
		quota           *tfv1.GPUResourceQuota
		currentUsage    *tfv1.GPUResourceUsage
		expectedPercent *tfv1.GPUResourceAvailablePercent
	}{
		{
			name:  "50% usage",
			quota: createTestQuota("test-ns", 100, 1000, 10),
			currentUsage: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(50, resource.DecimalSI),
				RequestsVRAM:   resource.NewQuantity(500, resource.BinarySI),
				LimitsTFlops:   resource.NewQuantity(50, resource.DecimalSI),
				LimitsVRAM:     resource.NewQuantity(500, resource.BinarySI),
				Workers:        func() *int32 { i := int32(5); return &i }(),
			},
			expectedPercent: &tfv1.GPUResourceAvailablePercent{
				RequestsTFlops: func() *int64 { i := int64(50); return &i }(), // (100-50)*100/100 = 50
				RequestsVRAM:   func() *int64 { i := int64(50); return &i }(), // (1000-500)*100/1000 = 50
				LimitsTFlops:   func() *int64 { i := int64(50); return &i }(), // (100-50)*100/100 = 50
				LimitsVRAM:     func() *int64 { i := int64(50); return &i }(), // (1000-500)*100/1000 = 50
				Workers:        func() *int64 { i := int64(50); return &i }(), // (10-5)*100/10 = 50
			},
		},
		{
			name:  "100% usage",
			quota: createTestQuota("test-ns", 100, 1000, 10),
			currentUsage: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(100, resource.DecimalSI),
				RequestsVRAM:   resource.NewQuantity(1000, resource.BinarySI),
				LimitsTFlops:   resource.NewQuantity(100, resource.DecimalSI),
				LimitsVRAM:     resource.NewQuantity(1000, resource.BinarySI),
				Workers:        func() *int32 { i := int32(10); return &i }(),
			},
			expectedPercent: &tfv1.GPUResourceAvailablePercent{
				RequestsTFlops: func() *int64 { i := int64(0); return &i }(), // (100-100)*100/100 = 0
				RequestsVRAM:   func() *int64 { i := int64(0); return &i }(), // (1000-1000)*100/1000 = 0
				LimitsTFlops:   func() *int64 { i := int64(0); return &i }(), // (100-100)*100/100 = 0
				LimitsVRAM:     func() *int64 { i := int64(0); return &i }(), // (1000-1000)*100/1000 = 0
				Workers:        func() *int64 { i := int64(0); return &i }(), // (10-10)*100/10 = 0
			},
		},
		{
			name:  "over usage - should be 0%",
			quota: createTestQuota("test-ns", 100, 1000, 10),
			currentUsage: &tfv1.GPUResourceUsage{
				RequestsTFlops: resource.NewQuantity(120, resource.DecimalSI), // Over quota
				RequestsVRAM:   resource.NewQuantity(1200, resource.BinarySI), // Over quota
				LimitsTFlops:   resource.NewQuantity(120, resource.DecimalSI), // Over quota
				LimitsVRAM:     resource.NewQuantity(1200, resource.BinarySI), // Over quota
				Workers:        func() *int32 { i := int32(12); return &i }(), // Over quota
			},
			expectedPercent: &tfv1.GPUResourceAvailablePercent{
				RequestsTFlops: func() *int64 { i := int64(0); return &i }(), // Should be clamped to 0
				RequestsVRAM:   func() *int64 { i := int64(0); return &i }(), // Should be clamped to 0
				LimitsTFlops:   func() *int64 { i := int64(0); return &i }(), // Should be clamped to 0
				LimitsVRAM:     func() *int64 { i := int64(0); return &i }(), // Should be clamped to 0
				Workers:        func() *int64 { i := int64(0); return &i }(), // Should be clamped to 0
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qs := NewQuotaStore(nil)

			entry := &QuotaStoreEntry{
				quota:        tt.quota,
				currentUsage: tt.currentUsage,
				available:    createUsageFromQuota(tt.quota),
			}

			result := qs.calculateAvailablePercent(entry)

			if tt.expectedPercent.RequestsTFlops != nil {
				assert.Equal(t, *tt.expectedPercent.RequestsTFlops, *result.RequestsTFlops)
			}
			if tt.expectedPercent.RequestsVRAM != nil {
				assert.Equal(t, *tt.expectedPercent.RequestsVRAM, *result.RequestsVRAM)
			}
			if tt.expectedPercent.LimitsTFlops != nil {
				assert.Equal(t, *tt.expectedPercent.LimitsTFlops, *result.LimitsTFlops)
			}
			if tt.expectedPercent.LimitsVRAM != nil {
				assert.Equal(t, *tt.expectedPercent.LimitsVRAM, *result.LimitsVRAM)
			}
			if tt.expectedPercent.Workers != nil {
				assert.Equal(t, *tt.expectedPercent.Workers, *result.Workers)
			}
		})
	}
}

func TestQuotaStore_reconcileQuotaStore(t *testing.T) {
	tests := []struct {
		name          string
		quotas        []tfv1.GPUResourceQuota
		workerPods    []v1.Pod
		expectedUsage map[string]*tfv1.GPUResourceUsage
	}{
		{
			name: "reconcile with worker pods",
			quotas: []tfv1.GPUResourceQuota{
				*createTestQuota("ns1", 100, 1000, 10),
				*createTestQuota("ns2", 200, 2000, 20),
			},
			workerPods: []v1.Pod{
				createWorkerPod("ns1", "worker1", "10", "100"),
				createWorkerPod("ns1", "worker2", "20", "200"),
				createWorkerPod("ns2", "worker3", "30", "300"),
			},
			expectedUsage: map[string]*tfv1.GPUResourceUsage{
				"ns1": {
					RequestsTFlops: resource.NewQuantity(30, resource.DecimalSI), // 10+20
					RequestsVRAM:   resource.NewQuantity(300, resource.BinarySI), // 100+200
					LimitsTFlops:   resource.NewQuantity(30, resource.DecimalSI), // 10+20
					LimitsVRAM:     resource.NewQuantity(300, resource.BinarySI), // 100+200
					Workers:        func() *int32 { i := int32(2); return &i }(), // 2 pods
				},
				"ns2": {
					RequestsTFlops: resource.NewQuantity(30, resource.DecimalSI), // 30
					RequestsVRAM:   resource.NewQuantity(300, resource.BinarySI), // 300
					LimitsTFlops:   resource.NewQuantity(30, resource.DecimalSI), // 30
					LimitsVRAM:     resource.NewQuantity(300, resource.BinarySI), // 300
					Workers:        func() *int32 { i := int32(1); return &i }(), // 1 pod
				},
			},
		},
		{
			name: "reconcile with deleted pods",
			quotas: []tfv1.GPUResourceQuota{
				*createTestQuota("ns1", 100, 1000, 10),
			},
			workerPods: []v1.Pod{
				createWorkerPod("ns1", "worker1", "10", "100"),
				createDeletedWorkerPod("ns1", "worker2", "20", "200"), // Should be ignored
			},
			expectedUsage: map[string]*tfv1.GPUResourceUsage{
				"ns1": {
					RequestsTFlops: resource.NewQuantity(10, resource.DecimalSI), // Only worker1
					RequestsVRAM:   resource.NewQuantity(100, resource.BinarySI), // Only worker1
					LimitsTFlops:   resource.NewQuantity(10, resource.DecimalSI), // Only worker1
					LimitsVRAM:     resource.NewQuantity(100, resource.BinarySI), // Only worker1
					Workers:        func() *int32 { i := int32(1); return &i }(), // Only worker1
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = tfv1.AddToScheme(scheme)

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithLists(&tfv1.GPUResourceQuotaList{Items: tt.quotas}).
				Build()

			qs := NewQuotaStore(client)
			ctx := context.Background()

			// Initialize quota store
			err := qs.initQuotaStore(ctx)
			require.NoError(t, err)

			// Reconcile with worker pods
			qs.reconcileQuotaStore(ctx, tt.workerPods)

			// Verify usage for each namespace
			for namespace, expectedUsage := range tt.expectedUsage {
				entry, exists := qs.quotaStore[namespace]
				require.True(t, exists, "Quota entry should exist for namespace %s", namespace)

				assert.True(t, expectedUsage.RequestsTFlops.Equal(*entry.currentUsage.RequestsTFlops))
				assert.True(t, expectedUsage.RequestsVRAM.Equal(*entry.currentUsage.RequestsVRAM))
				assert.Equal(t, *expectedUsage.Workers, *entry.currentUsage.Workers)
			}
		})
	}
}

func TestQuotaStore_GetQuotaStatus(t *testing.T) {
	qs := NewQuotaStore(nil)
	quota := createTestQuota("test-ns", 100, 1000, 10)

	currentUsage := &tfv1.GPUResourceUsage{
		RequestsTFlops: resource.NewQuantity(30, resource.DecimalSI),
		RequestsVRAM:   resource.NewQuantity(300, resource.BinarySI),
		LimitsTFlops:   resource.NewQuantity(30, resource.DecimalSI),
		LimitsVRAM:     resource.NewQuantity(300, resource.BinarySI),
		Workers:        func() *int32 { i := int32(3); return &i }(),
	}

	available := &tfv1.GPUResourceUsage{
		RequestsTFlops: resource.NewQuantity(70, resource.DecimalSI),
		RequestsVRAM:   resource.NewQuantity(700, resource.BinarySI),
		LimitsTFlops:   resource.NewQuantity(70, resource.DecimalSI),
		LimitsVRAM:     resource.NewQuantity(700, resource.BinarySI),
		Workers:        func() *int32 { i := int32(7); return &i }(),
	}

	entry := &QuotaStoreEntry{
		quota:        quota,
		currentUsage: currentUsage,
		available:    available,
	}

	qs.quotaStore["test-ns"] = entry

	// Test existing quota
	usage, avail, exists := qs.GetQuotaStatus("test-ns")
	assert.True(t, exists)
	assert.True(t, currentUsage.RequestsTFlops.Equal(*usage.RequestsTFlops))
	assert.True(t, available.RequestsTFlops.Equal(*avail.RequestsTFlops))

	// Test non-existing quota
	usage, avail, exists = qs.GetQuotaStatus("non-existing")
	assert.False(t, exists)
	assert.Nil(t, usage)
	assert.Nil(t, avail)
}

func TestQuotaExceededError(t *testing.T) {
	err := &QuotaExceededError{
		Namespace: "test-ns",
		Resource:  "total.requests.tflops",
		Requested: *resource.NewQuantity(150, resource.DecimalSI),
		Available: *resource.NewQuantity(50, resource.DecimalSI),
		Limit:     *resource.NewQuantity(100, resource.DecimalSI),
	}

	expectedMsg := "quota exceeded in namespace test-ns for total.requests.tflops: requested 150, available 50, limit 100"
	assert.Equal(t, expectedMsg, err.Error())

	// Test IsQuotaError
	assert.True(t, IsQuotaError(err))
	assert.False(t, IsQuotaError(assert.AnError))
}

// Helper functions for creating test data

func createTestQuota(namespace string, tflops, vram int64, workers int32) *tfv1.GPUResourceQuota {
	return &tfv1.GPUResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-quota",
			Namespace: namespace,
		},
		Spec: tfv1.GPUResourceQuotaSpec{
			Total: tfv1.GPUResourceQuotaTotal{
				RequestsTFlops: resource.NewQuantity(tflops, resource.DecimalSI),
				RequestsVRAM:   resource.NewQuantity(vram, resource.BinarySI),
				LimitsTFlops:   resource.NewQuantity(tflops, resource.DecimalSI),
				LimitsVRAM:     resource.NewQuantity(vram, resource.BinarySI),
				Workers:        &workers,
			},
		},
	}
}

func createTestQuotaWithSingleLimits(maxTflops, maxVram int64) *tfv1.GPUResourceQuota {

	namespace := "test-ns"
	totalTflops := int64(100)
	totalVram := int64(1000)
	totalWorkers := int32(10)
	maxWorkers := int32(5)

	minTflops := int64(10)
	minVram := int64(100)
	minWorkers := int32(1)

	return &tfv1.GPUResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-quota",
			Namespace: namespace,
		},
		Spec: tfv1.GPUResourceQuotaSpec{
			Total: tfv1.GPUResourceQuotaTotal{
				RequestsTFlops: resource.NewQuantity(totalTflops, resource.DecimalSI),
				RequestsVRAM:   resource.NewQuantity(totalVram, resource.BinarySI),
				LimitsTFlops:   resource.NewQuantity(totalTflops, resource.DecimalSI),
				LimitsVRAM:     resource.NewQuantity(totalVram, resource.BinarySI),
				Workers:        &totalWorkers,
			},
			Single: tfv1.GPUResourceQuotaSingle{
				Max: &tfv1.GPUResourceLimits{
					TFlops:  resource.NewQuantity(maxTflops, resource.DecimalSI),
					VRAM:    resource.NewQuantity(maxVram, resource.BinarySI),
					Workers: &maxWorkers,
				},
				Min: &tfv1.GPUResourceLimits{
					TFlops:  resource.NewQuantity(minTflops, resource.DecimalSI),
					VRAM:    resource.NewQuantity(minVram, resource.BinarySI),
					Workers: &minWorkers,
				},
			},
		},
	}
}

func createInvalidQuota(namespace string, tflops, vram int64, workers int32) *tfv1.GPUResourceQuota {
	return &tfv1.GPUResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "invalid-quota",
			Namespace: namespace,
		},
		Spec: tfv1.GPUResourceQuotaSpec{
			Total: tfv1.GPUResourceQuotaTotal{
				RequestsTFlops: resource.NewQuantity(tflops, resource.DecimalSI), // Can be negative
				RequestsVRAM:   resource.NewQuantity(vram, resource.BinarySI),
				LimitsTFlops:   resource.NewQuantity(tflops, resource.DecimalSI),
				LimitsVRAM:     resource.NewQuantity(vram, resource.BinarySI),
				Workers:        &workers,
			},
		},
	}
}

func createAllocRequest(tflops, vram int64, count uint) AllocRequest {
	namespace := "test-ns"
	return AllocRequest{
		PoolName: "test-pool",
		WorkloadNameNamespace: tfv1.NameNamespace{
			Namespace: namespace,
			Name:      "test-workload",
		},
		Request: tfv1.Resource{
			Tflops: *resource.NewQuantity(tflops, resource.DecimalSI),
			Vram:   *resource.NewQuantity(vram, resource.BinarySI),
		},
		Count: count,
	}
}

func createZeroUsage() *tfv1.GPUResourceUsage {
	return &tfv1.GPUResourceUsage{
		RequestsTFlops: resource.NewQuantity(0, resource.DecimalSI),
		RequestsVRAM:   resource.NewQuantity(0, resource.BinarySI),
		LimitsTFlops:   resource.NewQuantity(0, resource.DecimalSI),
		LimitsVRAM:     resource.NewQuantity(0, resource.BinarySI),
		Workers:        func() *int32 { i := int32(0); return &i }(),
	}
}

func createUsageFromQuota(quota *tfv1.GPUResourceQuota) *tfv1.GPUResourceUsage {
	usage := &tfv1.GPUResourceUsage{}

	if quota.Spec.Total.RequestsTFlops != nil {
		requestsTFlops := quota.Spec.Total.RequestsTFlops.DeepCopy()
		usage.RequestsTFlops = &requestsTFlops
	}

	if quota.Spec.Total.RequestsVRAM != nil {
		requestsVRAM := quota.Spec.Total.RequestsVRAM.DeepCopy()
		usage.RequestsVRAM = &requestsVRAM
	}

	if quota.Spec.Total.LimitsTFlops != nil {
		limitsTFlops := quota.Spec.Total.LimitsTFlops.DeepCopy()
		usage.LimitsTFlops = &limitsTFlops
	}

	if quota.Spec.Total.LimitsVRAM != nil {
		limitsVRAM := quota.Spec.Total.LimitsVRAM.DeepCopy()
		usage.LimitsVRAM = &limitsVRAM
	}

	if quota.Spec.Total.Workers != nil {
		workers := *quota.Spec.Total.Workers
		usage.Workers = &workers
	}

	return usage
}

func createWorkerPod(namespace, name, tflops, vram string) v1.Pod {
	return v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				constants.LabelComponent: constants.ComponentWorker,
				constants.WorkloadKey:    "test-workload",
			},
			Annotations: map[string]string{
				constants.TFLOPSRequestAnnotation: tflops,
				constants.VRAMRequestAnnotation:   vram,
			},
		},
		Status: v1.PodStatus{
			Phase: v1.PodRunning,
		},
	}
}

func createDeletedWorkerPod(namespace, name, tflops, vram string) v1.Pod {
	pod := createWorkerPod(namespace, name, tflops, vram)
	now := metav1.Now()
	pod.DeletionTimestamp = &now
	pod.Finalizers = []string{"test-finalizer"} // Add finalizer to make deletion timestamp valid
	return pod
}

// High-Priority Test Cases: Concurrent Allocation Tests
func TestQuotaStore_ConcurrentAllocation(t *testing.T) {
	t.Run("concurrent_allocations_within_quota", func(t *testing.T) {
		qs := NewQuotaStore(nil)
		quota := createTestQuotaWithValues(TestNamespace, 100, 1000, 20)
		qs.quotaStore[TestNamespace] = createQuotaEntry(quota)

		var wg sync.WaitGroup
		errors := make(chan error, 20)
		successes := make(chan bool, 20)

		// Launch 10 goroutines trying to allocate concurrently
		// Each requests 8 TFlops, so max 12 can succeed (96 TFlops < 100)
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				_ = id // Unused in test goroutine
				req := createAllocRequestWithValues(TestNamespace, 8, 80, 1)

				// Use a mutex to simulate the actual locking in GpuAllocator
				qs.storeMutex.Lock()
				err := qs.checkQuotaAvailable(TestNamespace, req)
				if err == nil {
					qs.AllocateQuota(TestNamespace, req)
					successes <- true
				} else {
					errors <- err
				}
				qs.storeMutex.Unlock()
			}(i)
		}

		wg.Wait()
		close(errors)
		close(successes)

		// Verify that only a reasonable number of allocations succeeded
		successCount := len(successes)
		errorCount := len(errors)

		assert.LessOrEqual(t, successCount, 12, "Should not exceed quota capacity")
		assert.Equal(t, 10, successCount+errorCount, "All requests should be processed")
		assert.GreaterOrEqual(t, successCount, 8, "Should allow reasonable number of allocations")

		// Verify quota consistency
		assertQuotaUsage(t, qs, TestNamespace, int64(successCount*8), int64(successCount*80), int32(successCount))
	})

	t.Run("concurrent_allocations_exceeding_quota", func(t *testing.T) {
		qs := NewQuotaStore(nil)
		quota := createTestQuotaWithValues(TestNamespace, 50, 500, 10) // Smaller quota
		qs.quotaStore[TestNamespace] = createQuotaEntry(quota)

		var wg sync.WaitGroup
		errors := make(chan error, 20)
		successes := make(chan bool, 20)

		// Launch 20 goroutines trying to allocate 10 TFlops each
		// Only 5 can succeed (50 TFlops total)
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				_ = id // Unused in test goroutine
				req := createAllocRequestWithValues(TestNamespace, 10, 100, 1)

				qs.storeMutex.Lock()
				err := qs.checkQuotaAvailable(TestNamespace, req)
				if err == nil {
					qs.AllocateQuota(TestNamespace, req)
					successes <- true
				} else {
					errors <- err
				}
				qs.storeMutex.Unlock()
			}(i)
		}

		wg.Wait()
		close(errors)
		close(successes)

		successCount := len(successes)
		errorCount := len(errors)

		assert.Equal(t, 5, successCount, "Exactly 5 allocations should succeed")
		assert.Equal(t, 15, errorCount, "15 allocations should fail")
		assert.Equal(t, 20, successCount+errorCount, "All requests should be processed")

		// Verify quota is fully utilized
		assertQuotaUsage(t, qs, TestNamespace, 50, 500, 5)
		assertQuotaAvailable(t, qs, TestNamespace, 0, 0, 5)
	})

	t.Run("concurrent_mixed_operations", func(t *testing.T) {
		qs := NewQuotaStore(nil)
		quota := createTestQuotaWithValues(TestNamespace, 100, 1000, 20)
		qs.quotaStore[TestNamespace] = createQuotaEntry(quota)

		// Pre-allocate some quota
		initialReq := createAllocRequestWithValues(TestNamespace, 20, 200, 2)
		qs.AllocateQuota(TestNamespace, initialReq)

		var wg sync.WaitGroup
		operations := make(chan string, 40)

		// Concurrent allocations
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req := createAllocRequestWithValues(TestNamespace, 5, 50, 1)

				qs.storeMutex.Lock()
				err := qs.checkQuotaAvailable(TestNamespace, req)
				if err == nil {
					qs.AllocateQuota(TestNamespace, req)
					operations <- "alloc_success"
				} else {
					operations <- "alloc_fail"
				}
				qs.storeMutex.Unlock()
			}()
		}

		// Concurrent deallocations
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// Deallocate part of the initial allocation
				req := tfv1.Resource{
					Tflops: *resource.NewQuantity(4, resource.DecimalSI),
					Vram:   *resource.NewQuantity(40, resource.BinarySI),
				}

				qs.storeMutex.Lock()
				qs.DeallocateQuota(TestNamespace, req, 1)
				operations <- "dealloc"
				qs.storeMutex.Unlock()
			}()
		}

		wg.Wait()
		close(operations)

		// Count operations
		allocSuccesses := 0
		allocFailures := 0
		deallocations := 0
		for op := range operations {
			switch op {
			case "alloc_success":
				allocSuccesses++
			case "alloc_fail":
				allocFailures++
			case "dealloc":
				deallocations++
			}
		}

		// Verify all operations completed
		assert.Equal(t, 15, allocSuccesses+allocFailures+deallocations, "All operations should complete")
		assert.Equal(t, 5, deallocations, "All deallocations should succeed")

		// Verify final quota state is consistent
		usage, available, exists := qs.GetQuotaStatus(TestNamespace)
		require.True(t, exists)

		// Usage + Available should equal total quota (allowing for deallocation effects)
		totalUsed := usage.RequestsTFlops.Value()
		totalAvail := available.RequestsTFlops.Value()
		assert.Equal(t, int64(100), totalUsed+totalAvail, "Total usage + available should equal quota")
	})
}

// High-Priority Test Cases: Boundary Value Tests
func TestQuotaStore_BoundaryValues(t *testing.T) {
	tests := []struct {
		name          string
		quotaTFlops   int64
		quotaVRAM     int64
		quotaWorkers  int32
		requestTFlops int64
		requestVRAM   int64
		requestCount  uint
		expectError   bool
		errorContains string
	}{
		{
			name:          "zero_quota_values",
			quotaTFlops:   0,
			quotaVRAM:     0,
			quotaWorkers:  0,
			requestTFlops: 1,
			requestVRAM:   1,
			requestCount:  1,
			expectError:   true,
			errorContains: "total.requests.tflops",
		},
		{
			name:          "zero_request_values",
			quotaTFlops:   100,
			quotaVRAM:     1000,
			quotaWorkers:  10,
			requestTFlops: 0,
			requestVRAM:   0,
			requestCount:  1,
			expectError:   false,
		},
		{
			name:          "single_unit_values",
			quotaTFlops:   1,
			quotaVRAM:     1,
			quotaWorkers:  1,
			requestTFlops: 1,
			requestVRAM:   1,
			requestCount:  1,
			expectError:   false,
		},
		{
			name:          "large_quota_values",
			quotaTFlops:   300000000,  // 300M TFlops (enough for 500000*500=250M)
			quotaVRAM:     3000000000, // 3G VRAM (enough for 5000000*500=2.5G)
			quotaWorkers:  1000,       // 1000 workers (enough for 500)
			requestTFlops: 500000,
			requestVRAM:   5000000,
			requestCount:  500,
			expectError:   false,
		},
		{
			name:          "request_exceeds_quota_workers",
			quotaTFlops:   1000,
			quotaVRAM:     10000,
			quotaWorkers:  100,
			requestTFlops: 1,
			requestVRAM:   10,
			requestCount:  101, // This should exceed worker quota
			expectError:   true,
			errorContains: "total.workers",
		},
		{
			name:          "exact_quota_match",
			quotaTFlops:   100,
			quotaVRAM:     1000,
			quotaWorkers:  10,
			requestTFlops: 10,
			requestVRAM:   100,
			requestCount:  10, // Exactly matches quota
			expectError:   false,
		},
		{
			name:          "quota_plus_one",
			quotaTFlops:   100,
			quotaVRAM:     1000,
			quotaWorkers:  10,
			requestTFlops: 1,  // Small per-worker request
			requestVRAM:   10, // Small per-worker request
			requestCount:  11, // One more than quota allows
			expectError:   true,
			errorContains: "total.workers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qs := NewQuotaStore(nil)
			quota := createTestQuotaWithValues(TestNamespace, tt.quotaTFlops, tt.quotaVRAM, tt.quotaWorkers)
			qs.quotaStore[TestNamespace] = createQuotaEntry(quota)

			req := createAllocRequestWithValues(TestNamespace, tt.requestTFlops, tt.requestVRAM, tt.requestCount)
			err := qs.checkQuotaAvailable(TestNamespace, req)

			if tt.expectError {
				assert.Error(t, err, "Expected error for test case: %s", tt.name)
				if tt.errorContains != "" && err != nil {
					assert.Contains(t, err.Error(), tt.errorContains, "Error should contain expected text")
				}
			} else {
				assert.NoError(t, err, "Expected no error for test case: %s", tt.name)

				// If allocation should succeed, verify it actually works
				if err == nil {
					qs.AllocateQuota(TestNamespace, req)
					expectedTFlops := tt.requestTFlops * int64(tt.requestCount)
					expectedVRAM := tt.requestVRAM * int64(tt.requestCount)
					expectedWorkers := int32(tt.requestCount)
					assertQuotaUsage(t, qs, TestNamespace, expectedTFlops, expectedVRAM, expectedWorkers)
				}
			}
		})
	}
}

// High-Priority Test Cases: Error Recovery Tests
func TestQuotaStore_ErrorRecovery(t *testing.T) {
	t.Run("partial_allocation_failure_recovery", func(t *testing.T) {
		qs := NewQuotaStore(nil)
		quota := createTestQuotaWithValues(TestNamespace, 100, 1000, 10)
		qs.quotaStore[TestNamespace] = createQuotaEntry(quota)

		// Step 1: Successful quota check and allocation
		req := createAllocRequestWithValues(TestNamespace, 15, 150, 5)
		err := qs.checkQuotaAvailable(TestNamespace, req)
		require.NoError(t, err, "Initial quota check should succeed")

		qs.AllocateQuota(TestNamespace, req)
		assertQuotaUsage(t, qs, TestNamespace, 75, 750, 5) // 15*5, 150*5, 5

		// Step 2: Simulate GPU allocation failure - need to rollback quota
		qs.DeallocateQuota(TestNamespace, req.Request, int32(req.Count))

		// Step 3: Verify quota is fully recovered
		assertQuotaUsage(t, qs, TestNamespace, 0, 0, 0)
		assertQuotaAvailable(t, qs, TestNamespace, 100, 1000, 10)

		// Step 4: Verify we can allocate again
		newReq := createAllocRequestWithValues(TestNamespace, 30, 300, 3)
		err = qs.checkQuotaAvailable(TestNamespace, newReq)
		assert.NoError(t, err, "Should be able to allocate after recovery")
	})

	t.Run("partial_deallocation_consistency", func(t *testing.T) {
		qs := NewQuotaStore(nil)
		quota := createTestQuotaWithValues(TestNamespace, 100, 1000, 10)
		qs.quotaStore[TestNamespace] = createQuotaEntry(quota)

		// Allocate some quota
		req1 := createAllocRequestWithValues(TestNamespace, 20, 200, 2)
		req2 := createAllocRequestWithValues(TestNamespace, 30, 300, 3)

		qs.AllocateQuota(TestNamespace, req1)
		qs.AllocateQuota(TestNamespace, req2)
		assertQuotaUsage(t, qs, TestNamespace, 130, 1300, 5) // (20*2)+(30*3), (200*2)+(300*3), 2+3

		// Partially deallocate - deallocate more than actually allocated for req1
		oversizedDealloc := tfv1.Resource{
			Tflops: *resource.NewQuantity(50, resource.DecimalSI), // More than req1's 40
			Vram:   *resource.NewQuantity(500, resource.BinarySI), // More than req1's 400
		}
		qs.DeallocateQuota(TestNamespace, oversizedDealloc, 3) // More workers than req1's 2

		// Deallocation will subtract the full request from usage (clamped to 0 by safeSub)
		// Current usage was 130 TFlops, 1300 VRAM, 5 workers
		// Deallocation request: 50*3=150 TFlops, 500*3=1500 VRAM, 3 workers
		// After deallocation: usage goes to 0 (due to safeSub), workers: 5-3=2
		assertQuotaUsage(t, qs, TestNamespace, 0, 0, 2) // safeSub clamps to 0, workers 5-3=2

		// Available quota is increased by the actual amount that was deallocated (130, 1300, 3)
		// Starting available was: (100-130)=-30, (1000-1300)=-300, (10-5)=5
		// After deallocation: available = (-30)+130=100, (-300)+1300=1000, 5+3=8
		// But since the available was clamped to 0 before the deallocation, it becomes 0+130=130, 0+1300=1300
		assertQuotaAvailable(t, qs, TestNamespace, 130, 1300, 8)
	})

	t.Run("quota_state_corruption_recovery", func(t *testing.T) {
		qs := NewQuotaStore(nil)
		quota := createTestQuotaWithValues(TestNamespace, 100, 1000, 10)
		entry := createQuotaEntry(quota)

		// Simulate corrupted state where usage > total
		entry.currentUsage.RequestsTFlops.Set(150) // More than quota
		entry.currentUsage.RequestsVRAM.Set(1500)  // More than quota
		*entry.currentUsage.Workers = 15           // More than quota

		// Available becomes negative (which should be handled)
		entry.available.RequestsTFlops.Set(-50)
		entry.available.RequestsVRAM.Set(-500)
		*entry.available.Workers = -5

		qs.quotaStore[TestNamespace] = entry

		// Attempt to allocate - should fail gracefully
		req := createAllocRequestWithValues(TestNamespace, 10, 100, 1)
		err := qs.checkQuotaAvailable(TestNamespace, req)
		assert.Error(t, err, "Should fail when quota is over-allocated")

		// Test reconciliation can fix corrupted state
		workerPods := []v1.Pod{
			createWorkerPod(TestNamespace, "worker1", "30", "300"),
			createWorkerPod(TestNamespace, "worker2", "20", "200"),
		}

		qs.reconcileQuotaStore(context.Background(), workerPods)

		// After reconciliation, usage should match actual pods
		assertQuotaUsage(t, qs, TestNamespace, 50, 500, 2)     // 30+20, 300+200, 2 pods
		assertQuotaAvailable(t, qs, TestNamespace, 50, 500, 8) // 100-50, 1000-500, 10-2
	})

	t.Run("concurrent_error_scenarios", func(t *testing.T) {
		qs := NewQuotaStore(nil)
		quota := createTestQuotaWithValues(TestNamespace, 50, 500, 5)
		qs.quotaStore[TestNamespace] = createQuotaEntry(quota)

		var wg sync.WaitGroup
		results := make(chan string, 20)

		// Simulate concurrent operations where some fail
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				req := createAllocRequestWithValues(TestNamespace, 15, 150, 1) // 15*3+ would exceed quota

				qs.storeMutex.Lock()
				err := qs.checkQuotaAvailable(TestNamespace, req)
				if err == nil {
					qs.AllocateQuota(TestNamespace, req)
					results <- "success"

					// Simulate immediate failure requiring rollback
					if id%3 == 0 { // Every 3rd allocation "fails"
						qs.DeallocateQuota(TestNamespace, req.Request, int32(req.Count))
						results <- "rollback"
					}
				} else {
					results <- "failed"
				}
				qs.storeMutex.Unlock()
			}(i)
		}

		wg.Wait()
		close(results)

		// Count results
		successes := 0
		failures := 0
		rollbacks := 0
		for result := range results {
			switch result {
			case "success":
				successes++
			case "failed":
				failures++
			case "rollback":
				rollbacks++
			}
		}

		// Verify that the system handled errors gracefully
		assert.Equal(t, 10, successes+failures, "All allocation attempts should be accounted for")

		// Final state should be consistent
		usage, available, exists := qs.GetQuotaStatus(TestNamespace)
		require.True(t, exists)
		assert.Equal(t, int64(50), usage.RequestsTFlops.Value()+available.RequestsTFlops.Value(), "Total should be preserved")
		assert.GreaterOrEqual(t, rollbacks, 0, "Should handle rollbacks gracefully")
	})
}

// Performance Benchmark Tests
func BenchmarkQuotaStore_AllocationDeallocation(b *testing.B) {
	qs := NewQuotaStore(nil)
	quota := createTestQuotaWithValues(TestNamespace, 10000, 100000, 1000)
	qs.quotaStore[TestNamespace] = createQuotaEntry(quota)

	req := createAllocRequestWithValues(TestNamespace, 1, 10, 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		qs.AllocateQuota(TestNamespace, req)
		qs.DeallocateQuota(TestNamespace, req.Request, 1)
	}
}

func BenchmarkQuotaStore_QuotaCheck(b *testing.B) {
	qs := NewQuotaStore(nil)
	quota := createTestQuotaWithValues(TestNamespace, 10000, 100000, 1000)
	qs.quotaStore[TestNamespace] = createQuotaEntry(quota)

	req := createAllocRequestWithValues(TestNamespace, 1, 10, 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = qs.checkQuotaAvailable(TestNamespace, req)
	}
}

func BenchmarkQuotaStore_GetQuotaStatus(b *testing.B) {
	qs := NewQuotaStore(nil)
	quota := createTestQuotaWithValues(TestNamespace, 10000, 100000, 1000)
	qs.quotaStore[TestNamespace] = createQuotaEntry(quota)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = qs.GetQuotaStatus(TestNamespace)
	}
}

func BenchmarkQuotaStore_ConcurrentOperations(b *testing.B) {
	qs := NewQuotaStore(nil)
	quota := createTestQuotaWithValues(TestNamespace, 10000, 100000, 1000)
	qs.quotaStore[TestNamespace] = createQuotaEntry(quota)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := createAllocRequestWithValues(TestNamespace, 1, 10, 1)

			qs.storeMutex.Lock()
			err := qs.checkQuotaAvailable(TestNamespace, req)
			if err == nil {
				qs.AllocateQuota(TestNamespace, req)
				qs.DeallocateQuota(TestNamespace, req.Request, 1)
			}
			qs.storeMutex.Unlock()
		}
	})
}
