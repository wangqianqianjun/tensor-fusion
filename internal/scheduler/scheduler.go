package scheduler

import (
	tfv1 "github.com/NexusGPU/tensor-fusion-operator/api/v1"
)

// Scheduler is the interface that wraps the basic scheduling methods.
type Scheduler interface {
	// Schedule schedules a request to a gpu.
	Schedule(request tfv1.Resource) (*tfv1.GPU, error)

	// Release releases a request from a gpu.
	Release(request tfv1.Resource, gpu *tfv1.GPU) error

	// OnAdd is called when a gpu is added.
	OnAdd(gpu *tfv1.GPU)
	// OnUpdate is called when a gpu is updated.
	OnUpdate(oldGPU, newGPU *tfv1.GPU)
	// OnDelete is called when a gpu is deleted.
	OnDelete(gpu *tfv1.GPU)
}
