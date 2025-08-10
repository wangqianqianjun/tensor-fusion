/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	"github.com/NexusGPU/tensor-fusion/internal/constants"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// GPUNodeSpec defines the desired state of GPUNode.
type GPUNodeSpec struct {

	// +kubebuilder:default=AutoSelect
	ManageMode GPUNodeManageMode `json:"manageMode,omitempty"`

	// +optional
	CostPerHour string `json:"costPerHour,omitempty"`

	// if not all GPU cards should be used, specify the GPU card indices, default to empty,
	// onboard all GPU cards to the pool
	// +optional
	GPUCardIndices []int `json:"gpuCardIndices,omitempty"`

	// +optional
	CloudVendorParam string `json:"cloudVendorParam,omitempty"`
}

// +kubebuilder:validation:Enum=Manual;AutoSelect;Provisioned
type GPUNodeManageMode string

const (
	GPUNodeManageModeManual      GPUNodeManageMode = "Manual"
	GPUNodeManageModeAutoSelect  GPUNodeManageMode = "AutoSelect"
	GPUNodeManageModeProvisioned GPUNodeManageMode = "Provisioned"
)

// GPUNodeStatus defines the observed state of GPUNode.
type GPUNodeStatus struct {
	// +kubebuilder:default=Pending
	Phase TensorFusionGPUNodePhase `json:"phase"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	TotalTFlops resource.Quantity `json:"totalTFlops"`
	TotalVRAM   resource.Quantity `json:"totalVRAM"`

	// +optional
	VirtualTFlops resource.Quantity `json:"virtualTFlops,omitempty"`
	// +optional
	VirtualVRAM resource.Quantity `json:"virtualVRAM,omitempty"`

	// +optional
	AvailableTFlops resource.Quantity `json:"availableTFlops,omitempty"`
	// +optional
	AvailableVRAM resource.Quantity `json:"availableVRAM,omitempty"`

	// +optional
	VirtualAvailableTFlops *resource.Quantity `json:"virtualAvailableTFlops,omitempty"`
	// +optional
	VirtualAvailableVRAM *resource.Quantity `json:"virtualAvailableVRAM,omitempty"`

	// +optional
	HypervisorStatus NodeHypervisorStatus `json:"hypervisorStatus,omitempty"`

	// +optional
	NodeInfo GPUNodeInfo `json:"nodeInfo,omitempty"`

	// +optional
	LoadedModels *[]string `json:"loadedModels,omitempty"`

	TotalGPUs   int32 `json:"totalGPUs"`
	ManagedGPUs int32 `json:"managedGPUs"`

	// +optional
	ManagedGPUDeviceIDs []string `json:"managedGPUDeviceIDs,omitempty"`

	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +optional
	AllocationInfo []*RunningAppDetail `json:"allocationInfo,omitempty"`
}

// +kubebuilder:validation:Enum=Pending;Provisioning;Migrating;Running;Succeeded;Failed;Unknown;Destroying
type TensorFusionGPUNodePhase string

const (
	TensorFusionGPUNodePhasePending    TensorFusionGPUNodePhase = constants.PhasePending
	TensorFusionGPUNodePhaseMigrating  TensorFusionGPUNodePhase = constants.PhaseMigrating
	TensorFusionGPUNodePhaseRunning    TensorFusionGPUNodePhase = constants.PhaseRunning
	TensorFusionGPUNodePhaseSucceeded  TensorFusionGPUNodePhase = constants.PhaseSucceeded
	TensorFusionGPUNodePhaseFailed     TensorFusionGPUNodePhase = constants.PhaseFailed
	TensorFusionGPUNodePhaseUnknown    TensorFusionGPUNodePhase = constants.PhaseUnknown
	TensorFusionGPUNodePhaseDestroying TensorFusionGPUNodePhase = constants.PhaseDestroying
)

type GPUNodeInfo struct {
	// Additional space for L1/L2 VRAM buffer
	RAMSize      resource.Quantity `json:"ramSize,omitempty"`
	DataDiskSize resource.Quantity `json:"dataDiskSize,omitempty"`
}

type NodeHypervisorStatus struct {
	HypervisorState   string      `json:"hypervisorState,omitempty"`
	HypervisorVersion string      `json:"hypervisorVersion,omitempty"`
	LastHeartbeatTime metav1.Time `json:"lastHeartbeatTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Total TFlops",type="string",JSONPath=".status.totalTFlops"
// +kubebuilder:printcolumn:name="Total VRAM",type="string",JSONPath=".status.totalVRAM"
// +kubebuilder:printcolumn:name="Virtual TFlops",type="string",JSONPath=".status.virtualTFlops"
// +kubebuilder:printcolumn:name="Virtual VRAM",type="string",JSONPath=".status.virtualVRAM"
// +kubebuilder:printcolumn:name="Available TFlops",type="string",JSONPath=".status.availableTFlops"
// +kubebuilder:printcolumn:name="Available VRAM",type="string",JSONPath=".status.availableVRAM"
// +kubebuilder:printcolumn:name="GPU Count",type="integer",JSONPath=".status.totalGPUs"
// GPUNode is the Schema for the gpunodes API.
type GPUNode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GPUNodeSpec   `json:"spec,omitempty"`
	Status GPUNodeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GPUNodeList contains a list of GPUNode.
type GPUNodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GPUNode `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GPUNode{}, &GPUNodeList{})
}
