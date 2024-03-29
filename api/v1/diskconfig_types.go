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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// DiskConfigSpec defines the desired state of DiskConfig
type DiskConfigSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// StorageClassName is the of the StorageClass required by the config.
	//+kubebuilder:validation:Optional
	StorageClassName string `json:"storageClassName,omitempty" yaml:"storageClassName,omitempty"`

	// Capacity represents the desired capacity of the underlying volume.
	//+kubebuilder:default:="1Gi"
	//+kubebuilder:validation:Optional
	Capacity resource.Quantity `json:"capacity,omitempty" yaml:"capacity,omitempty"`

	// MountPointPattern is the mount point of the disk. %d is optional and represents disk number in order. Will be automatically appended for second drive if missing.
	// Reserved characters: ><|:&.+*!?^$()[]{}, only 1 %d allowed.
	//+kubebuilder:default:="/media/discoblocks/<name>-%d"
	//+kubebuilder:validation:Pattern:="^/(.*)"
	//+kubebuilder:validation:Optional
	MountPointPattern string `json:"mountPointPattern,omitempty" yaml:"mountPointPattern,omitempty"`

	// AccessModes contains the desired access modes the volume should have.
	// More info: https://kubernetes.io/docs/concepts/storage/persistent-volumes#access-modes-1
	//+kubebuilder:default:={"ReadWriteOnce"}
	//+kubebuilder:validation:Optional
	AccessModes []corev1.PersistentVolumeAccessMode `json:"accessModes,omitempty" yaml:"accessModes,omitempty"`

	// AvailabilityMode defines the desired number of instances.
	//+kubebuilder:default:="ReadWriteOnce"
	//+kubebuilder:validation:Optional
	AvailabilityMode AvailabilityMode `json:"availabilityMode,omitempty" yaml:"availabilityMode,omitempty"`

	// NodeSelector is a selector which must be true for the disk to fit on a node. Selector which must match a node’s labels for the disk to be provisioned on that node.
	//+kubebuilder:validation:Optional
	NodeSelector *metav1.LabelSelector `json:"nodeSelector,omitempty" yaml:"nodeSelector,omitempty"`

	// PodSelector is a selector which must be true for the pod to attach disk.
	//+kubebuilder:validation:Required
	PodSelector map[string]string `json:"podSelector" yaml:"podSelector"`

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
	//+kubebuilder:default:="1000Gi"
	//+kubebuilder:validation:Optional
	MaximumCapacityOfDisk resource.Quantity `json:"maximumCapacityOfDisk,omitempty" yaml:"maximumCapacityOfDisk,omitempty"`

	// MaximumCapacityOfDisks defines maximum number of a disks.
	//+kubebuilder:default:=1
	//+kubebuilder:validation:Minimum:=1
	//+kubebuilder:validation:Maximum:=150
	//+kubebuilder:validation:Optional
	MaximumNumberOfDisks uint8 `json:"maximumNumberOfDisks,omitempty" yaml:"maximumNumberOfDisks,omitempty"`

	// ExtendCapacity represents the capacity to extend with.
	//+kubebuilder:default:="1Gi"
	//+kubebuilder:validation:Optional
	ExtendCapacity resource.Quantity `json:"extendCapacity,omitempty" yaml:"extendCapacity,omitempty"`

	// CoolDown defines temporary pause of scaling. Minimum: 10s
	//+kubebuilder:default:="5m"
	//+kubebuilder:validation:Optional
	CoolDown metav1.Duration `json:"coolDown,omitempty" yaml:"coolDown,omitempty"`

	// Pause disables autoscaling of disks.
	//+kubebuilder:default:=false
	//+kubebuilder:validation:Optional
	Pause bool `json:"pause,omitempty" yaml:"pause,omitempty"`
}

// DiskConfigStatus defines the observed state of DiskConfig
type DiskConfigStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Conditions is a list of status of all the disks.
	Conditions []metav1.Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
}

// +kubebuilder:validation:Enum=ReadWriteSame;ReadWriteOnce;ReadWriteDaemon
type AvailabilityMode string

const (
	ReadWriteSame   AvailabilityMode = "ReadWriteSame"
	ReadWriteOnce   AvailabilityMode = "ReadWriteOnce"
	ReadWriteDaemon AvailabilityMode = "ReadWriteDaemon"
)

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// DiskConfig is the Schema for the diskconfigs API
type DiskConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DiskConfigSpec   `json:"spec,omitempty"`
	Status DiskConfigStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// DiskConfigList contains a list of DiskConfig
type DiskConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DiskConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DiskConfig{}, &DiskConfigList{})
}
