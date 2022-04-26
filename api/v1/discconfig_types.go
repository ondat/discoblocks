/*
Copyright 2022.

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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// DiscConfigSpec defines the desired state of DiscConfig
type DiscConfigSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// StorageClassName is the of the StorageClass required by the config.
	//+kubebuilder:validation:Required
	StorageClassName string `json:"storageClassName" yaml:"storageClassName"`

	// Capacity represents the desired capacity of the underlying volume.
	//+kubebuilder:default:="1Gi"
	//+kubebuilder:validation:Pattern:="^([0-9]+)(m|Mi|g|Gi)$"
	//+kubebuilder:validation:Optional
	Capacity string `json:"capacity,omitempty" yaml:"capacity,omitempty"`

	// MountPointPattern is the mount point of the disk. {n} represents disk number in order.
	//+kubebuilder:default:="/media/discoblocks/{n}"
	//+kubebuilder:validation:Pattern:="^/(.*){n}(.*)"
	//+kubebuilder:validation:Optional
	MountPointPattern string `json:"mountPointPattern,omitempty" yaml:"mountPointPattern,omitempty"`

	// NodeSelector is a selector which must be true for the disk to fit on a node. Selector which must match a node’s labels for the disk to be provisioned on that node.
	//+kubebuilder:validation:Optional
	NodeSelector string `json:"nodeSelector,omitempty" yaml:"nodeSelector,omitempty"`

	// PrometheusEndpoint defines the metrics endpoint of disk capacity.
	//+kubebuilder:validation:Optional
	PrometheusEndpoint string `json:"prometheusEndpoint,omitempty" yaml:"prometheusEndpoint,omitempty"`

	// Policy contains the disk scale policies.
	Policy Policy `json:"policy,omitempty" yaml:"policy,omitempty"`
}

// Policy defines disk resize policies.
type Policy struct {
	// UpscaleTriggerPercentage defines the disk fullness percentage for disk expansion.
	//+kubebuilder:default:=80
	//+kubebuilder:validation:Minimum:=50
	//+kubebuilder:validation:Maximum:=100
	//+kubebuilder:validation:Optional
	UpscaleTriggerPercentage uint8 `json:"upscaleTriggerPercentage,omitempty" yaml:"upscaleTriggerPercentage,omitempty"`

	// MaximumCapacityOfDisks defines maximum capacity of a disk.
	//+kubebuilder:validation:Pattern:="^([0-9]+)(m|Mi|g|Gi)$"
	//+kubebuilder:validation:Optional
	MaximumCapacityOfDisk string `json:"maximumCapacityOfDisk,omitempty" yaml:"maximumCapacityOfDisk,omitempty"`

	// MaximumCapacityOfDisks defines maximum number of a disks.
	//+kubebuilder:default:=10
	//+kubebuilder:validation:Minimum:=1
	//+kubebuilder:validation:Maximum:=1000
	//+kubebuilder:validation:Optional
	MaximumNumberOfDisks uint8 `json:"maximumNumberOfDisks,omitempty" yaml:"maximumNumberOfDisks,omitempty"`
}

// DiscConfigStatus defines the observed state of DiscConfig
type DiscConfigStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Phase is the phase of the Discoblocks provisioning.
	Phase Phase `json:"phase,omitempty" yaml:"phase,omitempty"`

	// Conditions is a list of status of all the disks.
	Conditions []metav1.Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`

	Nodes map[string]string `json:"nodes,omitempty" yaml:"nodes,omitempty"`
}

// +kubebuilder:validation:Enum=Ready;Running
type Phase string

const (
	Ready   Phase = "Ready"
	Running Phase = "Running"
)

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// DiscConfig is the Schema for the discconfigs API
type DiscConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DiscConfigSpec   `json:"spec,omitempty"`
	Status DiscConfigStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// DiscConfigList contains a list of DiscConfig
type DiscConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DiscConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DiscConfig{}, &DiscConfigList{})
}
