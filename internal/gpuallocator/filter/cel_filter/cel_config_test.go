package cel_filter

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	tfv1 "github.com/NexusGPU/tensor-fusion/api/v1"
	"github.com/stretchr/testify/require"
)

// Test constants for CEL expressions (same as in cel_filter_test.go)
const (
	// Phase expressions
	testExamplePhaseRunning = `gpu.phase == 'Running'`

	// Resource expressions
	testExampleMinTFlops     = `gpu.available.tflops >= 0.5`
	testExampleSpecificModel = `gpu.gpuModel.contains('A100')`

	// Label expressions
	testExampleNVIDIAOnly = `gpu.gpuModel.startsWith('NVIDIA')`

	// Complex expressions
	testExampleComplex = `gpu.phase == 'Running' && gpu.available.tflops > 0.5 && size(gpu.runningApps) < 2`
)

func TestCELConfigManager_GetCELFiltersForPool(t *testing.T) {
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
				CELFilters: []tfv1.CELFilterConfig{
					{
						Name:       "high-priority",
						Expression: testExamplePhaseRunning,
						Priority:   100,
					},
					{
						Name:       "low-priority",
						Expression: testExampleNVIDIAOnly,
						Priority:   10,
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

	// Test CELConfigManager
	manager := NewCELConfigManager(fakeClient)
	celFilters, err := manager.GetCELFiltersForPool(context.Background(), pool.Name)
	require.NoError(t, err)
	require.Len(t, celFilters, 2)

	// Verify filters are sorted by priority (high to low)
	filterNames := make([]string, len(celFilters))
	for i, filter := range celFilters {
		filterNames[i] = filter.Name()
	}
	require.Equal(t, []string{"high-priority", "low-priority"}, filterNames)
}

func TestCELConfigManager_GetCELFiltersFromTemplate(t *testing.T) {
	// Create test scheme
	scheme := runtime.NewScheme()
	err := tfv1.AddToScheme(scheme)
	require.NoError(t, err)

	// Create test template
	schedulingTemplate := &tfv1.SchedulingConfigTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name: "direct-template",
		},
		Spec: tfv1.SchedulingConfigTemplateSpec{
			Placement: tfv1.PlacementConfig{
				CELFilters: []tfv1.CELFilterConfig{
					{
						Name:       "simple-filter",
						Expression: testExampleMinTFlops,
						Priority:   50,
					},
				},
			},
		},
	}

	// Create fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(schedulingTemplate).
		Build()

	// Test direct template access
	manager := NewCELConfigManager(fakeClient)
	celFilters, err := manager.GetCELFiltersFromTemplate(context.Background(), schedulingTemplate.Name)
	require.NoError(t, err)
	require.Len(t, celFilters, 1)
	require.Equal(t, "simple-filter", celFilters[0].Name())
}

func TestCELConfigManager_CreateCELFiltersFromConfig(t *testing.T) {
	manager := NewCELConfigManager(nil) // No client needed for this test

	celConfigs := []tfv1.CELFilterConfig{
		{
			Name:       "filter-3",
			Expression: testExamplePhaseRunning,
			Priority:   30,
		},
		{
			Name:       "filter-1",
			Expression: testExampleMinTFlops,
			Priority:   100,
		},
		{
			Name:       "filter-2",
			Expression: testExampleSpecificModel,
			Priority:   50,
		},
	}

	celFilters, err := manager.CreateCELFiltersFromConfig(celConfigs)
	require.NoError(t, err)
	require.Len(t, celFilters, 3)

	// Verify priority ordering (high to low)
	expectedOrder := []string{"filter-1", "filter-2", "filter-3"}
	actualOrder := make([]string, len(celFilters))
	for i, filter := range celFilters {
		actualOrder[i] = filter.Name()
	}
	require.Equal(t, expectedOrder, actualOrder)
}

func TestCELConfigManager_ValidateCELConfig(t *testing.T) {
	manager := NewCELConfigManager(nil)

	tests := []struct {
		name        string
		config      tfv1.CELFilterConfig
		expectError bool
	}{
		{
			name: "valid config",
			config: tfv1.CELFilterConfig{
				Name:       "valid",
				Expression: testExamplePhaseRunning,
				Priority:   100,
			},
			expectError: false,
		},
		{
			name: "invalid expression",
			config: tfv1.CELFilterConfig{
				Name:       "invalid",
				Expression: "gpu.phase ==", // Invalid syntax
				Priority:   100,
			},
			expectError: true,
		},
		{
			name: "complex valid expression",
			config: tfv1.CELFilterConfig{
				Name:       "complex",
				Expression: testExampleComplex,
				Priority:   100,
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.ValidateCELConfig(tt.config)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCELConfigManager_NoTemplate(t *testing.T) {
	// Create test scheme
	scheme := runtime.NewScheme()
	err := tfv1.AddToScheme(scheme)
	require.NoError(t, err)

	// Create pool without SchedulingConfigTemplate
	pool := &tfv1.GPUPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "no-template-pool",
		},
		Spec: tfv1.GPUPoolSpec{
			SchedulingConfigTemplate: nil, // No template specified
		},
	}

	// Create fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pool).
		Build()

	// Test that no CEL filters are returned
	manager := NewCELConfigManager(fakeClient)
	celFilters, err := manager.GetCELFiltersForPool(context.Background(), pool.Name)
	require.NoError(t, err)
	require.Len(t, celFilters, 0)
}

func TestCELConfigManager_EmptyConfig(t *testing.T) {
	manager := NewCELConfigManager(nil)

	// Test empty config slice
	celFilters, err := manager.CreateCELFiltersFromConfig([]tfv1.CELFilterConfig{})
	require.NoError(t, err)
	require.Len(t, celFilters, 0)
}
