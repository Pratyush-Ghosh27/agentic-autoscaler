/*
Copyright 2026.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgenticAutoscalerSpec defines the desired state of AgenticAutoscaler.
//
// All fields except TargetRef are optional. Optional scaling-behaviour fields
// (MaxStepSize, ScaleUpCooldownSeconds, ScaleDownCooldownSeconds,
// PreferredForecaster) default to nil meaning "defer to the classifier";
// see docs/design.md §8 for the full precedence chain.
type AgenticAutoscalerSpec struct {
	// TargetRef points at the Deployment to autoscale.
	// +kubebuilder:validation:Required
	TargetRef CrossVersionObjectReference `json:"targetRef"`

	// MinReplicas is the lower bound on replica count.
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=1
	// +optional
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// MaxReplicas is the upper bound on replica count.
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxReplicas *int32 `json:"maxReplicas,omitempty"`
}

// CrossVersionObjectReference identifies a target Deployment.
type CrossVersionObjectReference struct {
	// APIVersion of the target (e.g. "apps/v1").
	// +kubebuilder:validation:Required
	APIVersion string `json:"apiVersion"`

	// Kind of the target. Only "Deployment" is supported in this version.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Deployment
	Kind string `json:"kind"`

	// Name of the target.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// AgenticAutoscalerStatus defines the observed state of AgenticAutoscaler.
type AgenticAutoscalerStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// AgenticAutoscaler is the Schema for the agenticautoscalers API.
type AgenticAutoscaler struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgenticAutoscalerSpec   `json:"spec,omitempty"`
	Status AgenticAutoscalerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgenticAutoscalerList contains a list of AgenticAutoscaler.
type AgenticAutoscalerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgenticAutoscaler `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgenticAutoscaler{}, &AgenticAutoscalerList{})
}
