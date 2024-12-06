package scheduler

import (
	"fmt"
	"sync"

	tfv1 "github.com/NexusGPU/tensor-fusion-operator/api/v1"
)

// NaiveScheduler implements a simple scheduling strategy
type NaiveScheduler struct {
	sync.Mutex
	nodes map[string]*tfv1.GPUNode
}

// NewNaiveScheduler creates a new NaiveScheduler
func NewNaiveScheduler() *NaiveScheduler {
	return &NaiveScheduler{
		nodes: make(map[string]*tfv1.GPUNode),
	}
}

// Schedule implements Scheduler interface
func (s *NaiveScheduler) Schedule(request tfv1.Resource) (*tfv1.GPUNode, error) {
	s.Lock()
	defer s.Unlock()

	// Simple strategy: return the first node that has enough resources
	for _, node := range s.nodes {
		if node.Status.Available.Tflops.Cmp(request.Tflops) >= 0 &&
			node.Status.Available.Vram.Cmp(request.Vram) >= 0 {
			// Update the node's available resources
			node.Status.Available.Tflops.Sub(request.Tflops)
			node.Status.Available.Vram.Sub(request.Vram)
			return node, nil
		}
	}
	return nil, fmt.Errorf("no suitable node found for request: %v", request)
}

// OnAdd implements Scheduler interface
func (s *NaiveScheduler) OnAdd(node *tfv1.GPUNode) {
	s.Lock()
	defer s.Unlock()
	s.nodes[node.Name] = node
}

// OnUpdate implements Scheduler interface
func (s *NaiveScheduler) OnUpdate(oldNode, newNode *tfv1.GPUNode) {
	s.Lock()
	defer s.Unlock()
	s.nodes[newNode.Name] = newNode
}

// OnDelete implements Scheduler interface
func (s *NaiveScheduler) OnDelete(node *tfv1.GPUNode) {
	s.Lock()
	defer s.Unlock()
	delete(s.nodes, node.Name)
}

// Release implements Scheduler interface
func (s *NaiveScheduler) Release(request tfv1.Resource, node *tfv1.GPUNode) error {
	s.Lock()
	defer s.Unlock()

	existingNode, ok := s.nodes[node.Name]
	if !ok {
		return fmt.Errorf("node %s not found", node.Name)
	}

	// Add back the released resources
	existingNode.Status.Available.Tflops.Add(request.Tflops)
	existingNode.Status.Available.Vram.Add(request.Vram)
	// output the updated node
	node.Status.Available = existingNode.Status.Available
	return nil
}
