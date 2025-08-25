package cel_filter

import (
	"context"
	"fmt"
	"sort"

	tfv1 "github.com/NexusGPU/tensor-fusion/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CELConfigManager handles CEL filter configuration retrieval and creation
type CELConfigManager struct {
	client client.Client
}

// NewCELConfigManager creates a new CEL configuration manager
func NewCELConfigManager(client client.Client) *CELConfigManager {
	return &CELConfigManager{
		client: client,
	}
}

// GetCELFiltersForPool retrieves CEL filters from SchedulingConfigTemplate for a given pool
func (m *CELConfigManager) GetCELFiltersForPool(ctx context.Context, poolName string) ([]*CELFilter, error) {
	// Get pool to find SchedulingConfigTemplate
	pool := &tfv1.GPUPool{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: poolName}, pool); err != nil {
		return nil, fmt.Errorf("get pool %s: %w", poolName, err)
	}

	// If no SchedulingConfigTemplate is specified, return empty
	if pool.Spec.SchedulingConfigTemplate == nil {
		return nil, nil
	}

	return m.GetCELFiltersFromTemplate(ctx, *pool.Spec.SchedulingConfigTemplate)
}

// GetCELFiltersFromTemplate retrieves CEL filters directly from a SchedulingConfigTemplate
func (m *CELConfigManager) GetCELFiltersFromTemplate(ctx context.Context, templateName string) ([]*CELFilter, error) {
	// Get the SchedulingConfigTemplate
	schedulingConfigTemplate := &tfv1.SchedulingConfigTemplate{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: templateName}, schedulingConfigTemplate); err != nil {
		return nil, fmt.Errorf("get scheduling config template %s: %w", templateName, err)
	}

	return m.CreateCELFiltersFromConfig(schedulingConfigTemplate.Spec.Placement.CELFilters)
}

// CreateCELFiltersFromConfig creates CEL filters from configuration slice
func (m *CELConfigManager) CreateCELFiltersFromConfig(celConfigs []tfv1.CELFilterConfig) ([]*CELFilter, error) {
	if len(celConfigs) == 0 {
		return nil, nil
	}

	// Sort CEL configs by priority (higher priority first)
	sortedConfigs := make([]tfv1.CELFilterConfig, len(celConfigs))
	copy(sortedConfigs, celConfigs)
	sort.Slice(sortedConfigs, func(i, j int) bool {
		return sortedConfigs[i].Priority > sortedConfigs[j].Priority
	})

	// Create CEL filters
	var celFilters []*CELFilter
	for _, config := range sortedConfigs {
		celFilter, err := NewCELFilter(CELFilterConfig{
			Name:       config.Name,
			Expression: config.Expression,
			Priority:   config.Priority,
		})
		if err != nil {
			return nil, fmt.Errorf("create CEL filter %q: %w", config.Name, err)
		}
		celFilters = append(celFilters, celFilter)
	}

	return celFilters, nil
}

// ValidateCELConfig validates a CEL filter configuration
func (m *CELConfigManager) ValidateCELConfig(config tfv1.CELFilterConfig) error {
	// Try to create the filter to validate the expression
	_, err := NewCELFilter(CELFilterConfig{
		Name:       config.Name,
		Expression: config.Expression,
		Priority:   config.Priority,
	})
	return err
}
