package scheduler

import (
	"fmt"
	"sync"

	v1 "github.com/NexusGPU/tensor-fusion-operator/api/v1"
)

// NaiveScheduler implements a simple scheduling strategy
type NaiveScheduler struct {
	sync.RWMutex
	nodes map[string]*v1.GPUNode
}

// NewNaiveScheduler creates a new NaiveScheduler
func NewNaiveScheduler() *NaiveScheduler {
	return &NaiveScheduler{
		nodes: make(map[string]*v1.GPUNode),
	}
}

// Schedule implements Scheduler interface
func (s *NaiveScheduler) Schedule(request v1.Resource) (*v1.GPUNode, error) {
	s.RLock()
	defer s.RUnlock()

	// Simple strategy: return the first node that has enough resources
	for _, node := range s.nodes {
		if node.Status.Available.Tflops.Cmp(request.Tflops) >= 0 &&
			node.Status.Available.Vram.Cmp(request.Vram) >= 0 {
			return node, nil
		}
	}

	return nil, fmt.Errorf("no suitable node found for request: %v", request)
}

// OnAdd implements Scheduler interface
func (s *NaiveScheduler) OnAdd(node *v1.GPUNode) {
	s.Lock()
	defer s.Unlock()
	s.nodes[node.Name] = node
}

// OnUpdate implements Scheduler interface
func (s *NaiveScheduler) OnUpdate(oldNode, newNode *v1.GPUNode) {
	s.Lock()
	defer s.Unlock()
	s.nodes[newNode.Name] = newNode
}

// OnDelete implements Scheduler interface
func (s *NaiveScheduler) OnDelete(node *v1.GPUNode) {
	s.Lock()
	defer s.Unlock()
	delete(s.nodes, node.Name)
}
