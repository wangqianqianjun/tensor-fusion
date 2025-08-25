package cel_filter

import (
	"context"

	tfv1 "github.com/NexusGPU/tensor-fusion/api/v1"
	"github.com/NexusGPU/tensor-fusion/internal/gpuallocator/filter"
)

// CELFilterAdapter adapts CELFilter to implement filter.GPUFilter interface
type CELFilterAdapter struct {
	celFilter *CELFilter
}

// NewCELFilterAdapter creates a new adapter for CELFilter
func NewCELFilterAdapter(celFilter *CELFilter) filter.GPUFilter {
	return &CELFilterAdapter{
		celFilter: celFilter,
	}
}

// Filter implements the filter.GPUFilter interface
func (a *CELFilterAdapter) Filter(ctx context.Context, workerPodKey tfv1.NameNamespace, gpus []tfv1.GPU) ([]tfv1.GPU, error) {
	return a.celFilter.Filter(ctx, workerPodKey, gpus)
}

// Name implements the filter.GPUFilter interface
func (a *CELFilterAdapter) Name() string {
	return a.celFilter.Name()
}

// CreateCELFilterAdapters creates filter.GPUFilter adapters from CELFilter instances
func CreateCELFilterAdapters(celFilters []*CELFilter) []filter.GPUFilter {
	adapters := make([]filter.GPUFilter, len(celFilters))
	for i, celFilter := range celFilters {
		adapters[i] = NewCELFilterAdapter(celFilter)
	}
	return adapters
}
