package scheduler

import (
	v1 "github.com/NexusGPU/tensor-fusion-operator/api/v1"
)

// Scheduler is the interface that wraps the scheduling methods
type Scheduler interface {
	// Schedule takes a Resource Request and returns the pointer of the GPU node
	// that can accommodate the request. If no suitable node is found, it returns
	// an nil pointer and an error.
	Schedule(request v1.Resource) (*v1.GPUNode, error)

	// OnAdd is called when a new node is added
	OnAdd(node *v1.GPUNode)
	// OnUpdate is called when a node is modified
	OnUpdate(oldNode, newNode *v1.GPUNode)
	// OnDelete is called when a node is deleted
	OnDelete(node *v1.GPUNode)
}
