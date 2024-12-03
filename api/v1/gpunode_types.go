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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GPUNodeStatus defines the observed state of GPUNode.
type GPUNodeStatus struct {
	Capacity  Resource `json:"capacity"`
	Available Resource `json:"available"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// GPUNode is the Schema for the gpunodes API.
type GPUNode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

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
