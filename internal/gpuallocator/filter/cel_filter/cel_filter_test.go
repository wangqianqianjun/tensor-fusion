package cel_filter

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tfv1 "github.com/NexusGPU/tensor-fusion/api/v1"
	"github.com/stretchr/testify/require"
)

// Test constants for CEL expressions
const (
	// Phase expressions
	ExamplePhaseRunning = `gpu.phase == 'Running'`
	ExamplePhasePending = `gpu.phase == 'Pending'`

	// Resource expressions
	ExampleMinTFlops     = `gpu.available.tflops >= 0.5`
	ExampleMinVRAM       = `gpu.available.vram >= 4294967296` // 4GB in bytes
	ExampleResourceRatio = `gpu.available.tflops > gpu.capacity.tflops * 0.5`

	// Model expressions
	ExampleNVIDIAOnly    = `gpu.gpuModel.startsWith('NVIDIA')`
	ExampleSpecificModel = `gpu.gpuModel.contains('A100')`

	// Label expressions
	ExampleHasLabel   = `'gpu-tier' in gpu.labels`
	ExampleLabelValue = `gpu.labels != null && 'gpu-tier' in gpu.labels && gpu.labels['gpu-tier'] == 'premium'`

	// Load balancing expressions
	ExampleLowLoad = `size(gpu.runningApps) < 3`
	ExampleNoApps  = `size(gpu.runningApps) == 0`

	// Complex expressions
	ExampleComplex = `gpu.phase == 'Running' && gpu.available.tflops > 0.5 && size(gpu.runningApps) < 2`
)

func TestNewCELFilter(t *testing.T) {
	tests := []struct {
		name        string
		config      CELFilterConfig
		expectError bool
	}{
		{
			name: "valid basic expression",
			config: CELFilterConfig{
				Name:       "basic-test",
				Expression: ExamplePhaseRunning,
				Priority:   100,
			},
			expectError: false,
		},
		{
			name: "valid resource expression",
			config: CELFilterConfig{
				Name:       "resource-test",
				Expression: ExampleMinTFlops,
				Priority:   50,
			},
			expectError: false,
		},
		{
			name: "invalid expression syntax",
			config: CELFilterConfig{
				Name:       "invalid-test",
				Expression: "gpu.phase ==", // Invalid syntax
				Priority:   10,
			},
			expectError: true,
		},
		{
			name: "expression with labels",
			config: CELFilterConfig{
				Name:       "label-test",
				Expression: ExampleHasLabel,
				Priority:   75,
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := NewCELFilter(tt.config)
			if tt.expectError {
				require.Error(t, err)
				require.Nil(t, filter)
			} else {
				require.NoError(t, err)
				require.NotNil(t, filter)
				require.Equal(t, tt.config.Name, filter.Name())
			}
		})
	}
}

func TestCELFilter_Filter(t *testing.T) {
	// Create test GPUs
	gpus := []tfv1.GPU{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gpu-1",
				Namespace: "default",
				Labels: map[string]string{
					"gpu-tier": "premium",
				},
			},
			Status: tfv1.GPUStatus{
				Phase:    tfv1.TensorFusionGPUPhaseRunning,
				GPUModel: "NVIDIA A100",
				UUID:     "gpu-1-uuid",
				Capacity: &tfv1.Resource{
					Tflops: resource.MustParse("1.5"),
					Vram:   resource.MustParse("80Gi"),
				},
				Available: &tfv1.Resource{
					Tflops: resource.MustParse("1.0"),
					Vram:   resource.MustParse("60Gi"),
				},
				RunningApps: []*tfv1.RunningAppDetail{
					{
						Name:      "app-1",
						Namespace: "default",
						Count:     1,
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gpu-2",
				Namespace: "default",
				Labels: map[string]string{
					"gpu-tier": "basic",
				},
			},
			Status: tfv1.GPUStatus{
				Phase:    tfv1.TensorFusionGPUPhaseRunning,
				GPUModel: "NVIDIA RTX 4090",
				UUID:     "gpu-2-uuid",
				Capacity: &tfv1.Resource{
					Tflops: resource.MustParse("0.8"),
					Vram:   resource.MustParse("24Gi"),
				},
				Available: &tfv1.Resource{
					Tflops: resource.MustParse("0.2"),
					Vram:   resource.MustParse("8Gi"),
				},
				RunningApps: []*tfv1.RunningAppDetail{
					{
						Name:      "app-2",
						Namespace: "default",
						Count:     1,
					},
					{
						Name:      "app-3",
						Namespace: "default",
						Count:     2,
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gpu-3",
				Namespace: "default",
			},
			Status: tfv1.GPUStatus{
				Phase:    tfv1.TensorFusionGPUPhasePending,
				GPUModel: "NVIDIA A100",
				UUID:     "gpu-3-uuid",
				Capacity: &tfv1.Resource{
					Tflops: resource.MustParse("1.5"),
					Vram:   resource.MustParse("80Gi"),
				},
				Available: &tfv1.Resource{
					Tflops: resource.MustParse("1.5"),
					Vram:   resource.MustParse("80Gi"),
				},
			},
		},
	}

	workerPodKey := tfv1.NameNamespace{
		Name:      "test-pod",
		Namespace: "default",
	}

	tests := []struct {
		name         string
		expression   string
		expectedGPUs []string // GPU names that should pass the filter
		expectError  bool
	}{
		{
			name:         "filter by phase",
			expression:   ExamplePhaseRunning,
			expectedGPUs: []string{"gpu-1", "gpu-2"},
		},
		{
			name:         "filter by available resources",
			expression:   ExampleMinTFlops,
			expectedGPUs: []string{"gpu-1", "gpu-3"},
		},
		{
			name:         "filter by GPU model",
			expression:   "gpu.gpuModel.startsWith('NVIDIA A100')",
			expectedGPUs: []string{"gpu-1", "gpu-3"},
		},
		{
			name:         "filter by labels",
			expression:   ExampleLabelValue,
			expectedGPUs: []string{"gpu-1"},
		},
		{
			name:         "filter by running apps count",
			expression:   ExampleLowLoad,
			expectedGPUs: []string{"gpu-1", "gpu-2", "gpu-3"},
		},
		{
			name:         "complex filter",
			expression:   ExampleComplex,
			expectedGPUs: []string{"gpu-1"},
		},
		{
			name:         "filter none",
			expression:   "false",
			expectedGPUs: []string{},
		},
		{
			name:         "filter all",
			expression:   "true",
			expectedGPUs: []string{"gpu-1", "gpu-2", "gpu-3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := NewCELFilter(CELFilterConfig{
				Name:       tt.name,
				Expression: tt.expression,
				Priority:   100,
			})
			require.NoError(t, err)

			filteredGPUs, err := filter.Filter(context.Background(), workerPodKey, gpus)
			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Len(t, filteredGPUs, len(tt.expectedGPUs))

			// Check that the correct GPUs were filtered
			actualNames := make([]string, len(filteredGPUs))
			for i, gpu := range filteredGPUs {
				actualNames[i] = gpu.Name
			}

			require.ElementsMatch(t, tt.expectedGPUs, actualNames)
		})
	}
}

func TestCELFilter_UpdateExpression(t *testing.T) {
	// Create initial filter
	filter, err := NewCELFilter(CELFilterConfig{
		Name:       "update-test",
		Expression: ExamplePhaseRunning,
		Priority:   100,
	})
	require.NoError(t, err)

	// Test valid update
	err = filter.UpdateExpression(ExamplePhasePending)
	require.NoError(t, err)

	// Test invalid update
	err = filter.UpdateExpression("gpu.phase ==")
	require.Error(t, err)
}

func TestCELFilter_ThreadSafety(t *testing.T) {
	filter, err := NewCELFilter(CELFilterConfig{
		Name:       "thread-safety-test",
		Expression: ExamplePhaseRunning,
		Priority:   100,
	})
	require.NoError(t, err)

	// Create test GPU
	gpu := tfv1.GPU{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gpu-1",
			Namespace: "default",
		},
		Status: tfv1.GPUStatus{
			Phase: tfv1.TensorFusionGPUPhaseRunning,
		},
	}

	workerPodKey := tfv1.NameNamespace{
		Name:      "test-pod",
		Namespace: "default",
	}

	// Run concurrent operations
	done := make(chan bool, 3)

	// Concurrent filtering
	go func() {
		defer func() { done <- true }()
		for i := 0; i < 100; i++ {
			_, err := filter.Filter(context.Background(), workerPodKey, []tfv1.GPU{gpu})
			require.NoError(t, err)
		}
	}()

	// Concurrent name access
	go func() {
		defer func() { done <- true }()
		for i := 0; i < 100; i++ {
			name := filter.Name()
			require.Equal(t, "thread-safety-test", name)
		}
	}()

	// Concurrent expression updates
	go func() {
		defer func() { done <- true }()
		for i := 0; i < 10; i++ {
			err := filter.UpdateExpression(ExamplePhasePending)
			require.NoError(t, err)
			err = filter.UpdateExpression(ExamplePhaseRunning)
			require.NoError(t, err)
		}
	}()

	// Wait for all goroutines to complete
	for i := 0; i < 3; i++ {
		<-done
	}
}
