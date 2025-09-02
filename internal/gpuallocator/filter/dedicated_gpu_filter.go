package filter

import (
	"context"

	tfv1 "github.com/NexusGPU/tensor-fusion/api/v1"
	"github.com/samber/lo"
)

// DedicatedGPUFilter filters GPUs to only include those that are completely unallocated
type DedicatedGPUFilter struct{}

// NewDedicatedGPUFilter creates a new DedicatedGPUFilter
func NewDedicatedGPUFilter() *DedicatedGPUFilter {
	return &DedicatedGPUFilter{}
}

// Filter implements GPUFilter.Filter
func (f *DedicatedGPUFilter) Filter(ctx context.Context, workerPodKey tfv1.NameNamespace, gpus []*tfv1.GPU) ([]*tfv1.GPU, error) {
	return lo.Filter(gpus, func(gpu *tfv1.GPU, _ int) bool {
		// Check if GPU is completely unallocated (available == capacity)
		if gpu.Status.Available == nil || gpu.Status.Capacity == nil {
			return false
		}

		// Check if both TFlops and VRAM are fully available
		tflopsFullyAvailable := gpu.Status.Available.Tflops.Equal(gpu.Status.Capacity.Tflops)
		vramFullyAvailable := gpu.Status.Available.Vram.Equal(gpu.Status.Capacity.Vram)

		return tflopsFullyAvailable && vramFullyAvailable
	}), nil
}

func (f *DedicatedGPUFilter) Name() string {
	return "DedicatedGPUFilter"
}
