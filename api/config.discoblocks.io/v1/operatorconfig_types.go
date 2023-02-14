package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	cfg "sigs.k8s.io/controller-runtime/pkg/config/v1alpha1"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// OperatorConfig is the Schema for the operatorconfigs API
type OperatorConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	cfg.ControllerManagerConfigurationSpec `json:",inline"`

	// SupportedCsiDrivers list of supported CSI driver IDs
	SupportedCsiDrivers []string `json:"supportedCsiDrivers,omitempty"`

	// JobContainerImage is the container image for volume management operations
	JobContainerImage string `json:"jobContainerImage,omitempty"`

	// JobContainerImage is the container image of volume metrics sidecar
	ProxyContainerImage string `json:"proxyContainerImage,omitempty"`

	// SchedulerStrictMode defines scheduler's behavior on case of Discoblock errors
	SchedulerStrictMode bool `json:"schedulerStrictMode,omitempty"`

	// MutatorStrictMode defines mutator's behavior on case of Discoblock errors
	MutatorStrictMode bool `json:"mutatorStrictMode,omitempty"`
}

func init() {
	SchemeBuilder.Register(&OperatorConfig{})
}
