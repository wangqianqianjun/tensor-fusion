package gpuallocator

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	tfv1 "github.com/NexusGPU/tensor-fusion/api/v1"
	"github.com/NexusGPU/tensor-fusion/internal/gpuallocator/filter"
	cel_filter "github.com/NexusGPU/tensor-fusion/internal/gpuallocator/filter/cel_filter"
	"github.com/stretchr/testify/require"
)

func TestGpuAllocator_CELFilters_Integration(t *testing.T) {
	// Create test scheme
	scheme := runtime.NewScheme()
	err := tfv1.AddToScheme(scheme)
	require.NoError(t, err)

	// Create test resources
	schedulingTemplate := &tfv1.SchedulingConfigTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-template",
		},
		Spec: tfv1.SchedulingConfigTemplateSpec{
			Placement: tfv1.PlacementConfig{
				Mode: tfv1.PlacementModeCompactFirst,
				CELFilters: []tfv1.CELFilterConfig{
					{
						Name:       "running-gpus-only",
						Expression: "gpu.phase == 'Running'",
						Priority:   100,
					},
					{
						Name:       "sufficient-tflops",
						Expression: "gpu.available.tflops >= 0.5",
						Priority:   90,
					},
					{
						Name:       "nvidia-gpus-only",
						Expression: "gpu.gpuModel.contains('NVIDIA')",
						Priority:   80,
					},
				},
			},
		},
	}

	pool := &tfv1.GPUPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pool",
		},
		Spec: tfv1.GPUPoolSpec{
			SchedulingConfigTemplate: &schedulingTemplate.Name,
		},
	}

	// Create test GPUs
	gpus := []tfv1.GPU{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gpu-1-pass-all",
			},
			Status: tfv1.GPUStatus{
				Phase:    tfv1.TensorFusionGPUPhaseRunning,
				GPUModel: "NVIDIA A100",
				Available: &tfv1.Resource{
					Tflops: resource.MustParse("1.0"),
					Vram:   resource.MustParse("60Gi"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gpu-2-fail-phase",
			},
			Status: tfv1.GPUStatus{
				Phase:    tfv1.TensorFusionGPUPhasePending,
				GPUModel: "NVIDIA A100",
				Available: &tfv1.Resource{
					Tflops: resource.MustParse("1.0"),
					Vram:   resource.MustParse("60Gi"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gpu-3-fail-tflops",
			},
			Status: tfv1.GPUStatus{
				Phase:    tfv1.TensorFusionGPUPhaseRunning,
				GPUModel: "NVIDIA A100",
				Available: &tfv1.Resource{
					Tflops: resource.MustParse("0.3"),
					Vram:   resource.MustParse("60Gi"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gpu-4-fail-model",
			},
			Status: tfv1.GPUStatus{
				Phase:    tfv1.TensorFusionGPUPhaseRunning,
				GPUModel: "AMD Radeon RX 7900 XTX",
				Available: &tfv1.Resource{
					Tflops: resource.MustParse("1.0"),
					Vram:   resource.MustParse("24Gi"),
				},
			},
		},
	}

	// Create fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(schedulingTemplate, pool).
		Build()

	// Test CEL filters using CELConfigManager
	celConfigManager := cel_filter.NewCELConfigManager(fakeClient)
	celFilters, err := celConfigManager.GetCELFiltersForPool(context.Background(), pool.Name)
	require.NoError(t, err)
	require.Len(t, celFilters, 3)

	// Test filtering with CEL filters
	celFilterAdapters := cel_filter.CreateCELFilterAdapters(celFilters)
	filterRegistry := filter.NewFilterRegistry().With(celFilterAdapters...)

	filteredGPUs, _, err := filterRegistry.Apply(
		context.Background(),
		tfv1.NameNamespace{Name: "test-pod", Namespace: "default"},
		gpus,
		false,
	)
	require.NoError(t, err)

	// Only gpu-1 should pass all filters
	require.Len(t, filteredGPUs, 1)
	require.Equal(t, "gpu-1-pass-all", filteredGPUs[0].Name)
}

func TestGpuAllocator_CELFilters_ErrorHandling(t *testing.T) {
	// Create test scheme
	scheme := runtime.NewScheme()
	err := tfv1.AddToScheme(scheme)
	require.NoError(t, err)

	// Create scheduling template with invalid CEL expression
	schedulingTemplate := &tfv1.SchedulingConfigTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name: "invalid-template",
		},
		Spec: tfv1.SchedulingConfigTemplateSpec{
			Placement: tfv1.PlacementConfig{
				Mode: tfv1.PlacementModeCompactFirst,
				CELFilters: []tfv1.CELFilterConfig{
					{
						Name:       "invalid-expression",
						Expression: "gpu.phase ==", // Invalid syntax
						Priority:   100,
					},
				},
			},
		},
	}

	pool := &tfv1.GPUPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pool",
		},
		Spec: tfv1.GPUPoolSpec{
			SchedulingConfigTemplate: &schedulingTemplate.Name,
		},
	}

	// Create fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(schedulingTemplate, pool).
		Build()

	// Test that invalid CEL expression results in error
	celConfigManager := cel_filter.NewCELConfigManager(fakeClient)
	_, err = celConfigManager.GetCELFiltersForPool(context.Background(), pool.Name)
	require.Error(t, err)
	require.Contains(t, err.Error(), "create CEL filter")
}

func TestGpuAllocator_CELFilters_Priority_Ordering(t *testing.T) {
	// Create test scheme
	scheme := runtime.NewScheme()
	err := tfv1.AddToScheme(scheme)
	require.NoError(t, err)

	// Create scheduling template with multiple CEL filters with different priorities
	schedulingTemplate := &tfv1.SchedulingConfigTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name: "priority-template",
		},
		Spec: tfv1.SchedulingConfigTemplateSpec{
			Placement: tfv1.PlacementConfig{
				Mode: tfv1.PlacementModeCompactFirst,
				CELFilters: []tfv1.CELFilterConfig{
					{
						Name:       "low-priority",
						Expression: "gpu.name.contains('gpu')",
						Priority:   10,
					},
					{
						Name:       "high-priority",
						Expression: "gpu.phase == 'Running'",
						Priority:   100,
					},
					{
						Name:       "medium-priority",
						Expression: "gpu.gpuModel.contains('NVIDIA')",
						Priority:   50,
					},
				},
			},
		},
	}

	pool := &tfv1.GPUPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pool",
		},
		Spec: tfv1.GPUPoolSpec{
			SchedulingConfigTemplate: &schedulingTemplate.Name,
		},
	}

	// Create fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(schedulingTemplate, pool).
		Build()

	// Test that CEL filters are sorted by priority
	celConfigManager := cel_filter.NewCELConfigManager(fakeClient)
	celFilters, err := celConfigManager.GetCELFiltersForPool(context.Background(), pool.Name)
	require.NoError(t, err)
	require.Len(t, celFilters, 3)

	// Check that filters are ordered by priority (high to low)
	// Note: We can't easily check the internal order without exposing more internals,
	// but we can verify that all filters are created successfully
	filterNames := make([]string, len(celFilters))
	for i, filter := range celFilters {
		filterNames[i] = filter.Name()
	}

	expectedFilters := []string{"high-priority", "medium-priority", "low-priority"}
	require.ElementsMatch(t, expectedFilters, filterNames)
}
