package scheduler

import (
	"testing"

	v1 "github.com/NexusGPU/tensor-fusion-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func createGPUNode(name string, tflops, vram string) *v1.GPUNode {
	return &v1.GPUNode{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: v1.GPUNodeStatus{
			Available: v1.Resource{
				Tflops: resource.MustParse(tflops),
				Vram:   resource.MustParse(vram),
			},
		},
	}
}

func createRequest(tflops, vram string) v1.Resource {
	return v1.Resource{
		Tflops: resource.MustParse(tflops),
		Vram:   resource.MustParse(vram),
	}
}

func TestNaiveScheduler_Schedule(t *testing.T) {
	tests := []struct {
		name      string
		nodes     []*v1.GPUNode
		request   v1.Resource
		wantNode  string
		wantError bool
	}{
		{
			name: "simple match",
			nodes: []*v1.GPUNode{
				createGPUNode("node1", "100", "16Gi"),
			},
			request:   createRequest("50", "8Gi"),
			wantNode:  "node1",
			wantError: false,
		},
		{
			name: "no nodes",
			nodes: []*v1.GPUNode{},
			request:   createRequest("50", "8Gi"),
			wantNode:  "",
			wantError: true,
		},
		{
			name: "insufficient resources",
			nodes: []*v1.GPUNode{
				createGPUNode("node1", "40", "16Gi"),
			},
			request:   createRequest("50", "8Gi"),
			wantNode:  "",
			wantError: true,
		},
		{
			name: "multiple nodes, first fit",
			nodes: []*v1.GPUNode{
				createGPUNode("node1", "40", "16Gi"),
				createGPUNode("node2", "100", "32Gi"),
			},
			request:   createRequest("50", "8Gi"),
			wantNode:  "node2",
			wantError: false,
		},
		{
			name: "exact match",
			nodes: []*v1.GPUNode{
				createGPUNode("node1", "50", "8Gi"),
			},
			request:   createRequest("50", "8Gi"),
			wantNode:  "node1",
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewNaiveScheduler()
			
			// Add nodes
			for _, node := range tt.nodes {
				s.OnAdd(node)
			}

			// Try to schedule
			got, err := s.Schedule(tt.request)
			
			// Check error
			if (err != nil) != tt.wantError {
				t.Errorf("Schedule() error = %v, wantError %v", err, tt.wantError)
				return
			}

			// Check result
			if !tt.wantError {
				if got == nil {
					t.Error("Schedule() returned nil node when error not expected")
					return
				}
				if got.Name != tt.wantNode {
					t.Errorf("Schedule() got node = %v, want %v", got.Name, tt.wantNode)
				}
			}
		})
	}
}

func TestNaiveScheduler_NodeOperations(t *testing.T) {
	s := NewNaiveScheduler()
	node1 := createGPUNode("node1", "100", "16Gi")
	request := createRequest("50", "8Gi")

	// Test OnAdd
	s.OnAdd(node1)
	got, err := s.Schedule(request)
	if err != nil || got.Name != "node1" {
		t.Errorf("After OnAdd: Schedule() got = %v, want node1", got)
	}

	// Test OnUpdate
	node1Updated := createGPUNode("node1", "40", "16Gi")
	s.OnUpdate(node1, node1Updated)
	got, err = s.Schedule(request)
	if err == nil {
		t.Error("After OnUpdate: Schedule() should fail with insufficient resources")
	}

	// Test OnDelete
	s.OnDelete(node1Updated)
	got, err = s.Schedule(request)
	if err == nil {
		t.Error("After OnDelete: Schedule() should fail with no nodes")
	}
}
