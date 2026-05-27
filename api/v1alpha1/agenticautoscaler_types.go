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

	// RpsPerPodMin is the safety floor for the self-calibrating
	// rps_per_pod estimate. See docs/design.md §5 step 5.
	// +kubebuilder:default=50
	// +kubebuilder:validation:Minimum=1
	// +optional
	RpsPerPodMin *int32 `json:"rpsPerPodMin,omitempty"`

	// RpsPerPodMax is the safety ceiling for the self-calibrating
	// rps_per_pod estimate.
	// +kubebuilder:default=500
	// +kubebuilder:validation:Minimum=1
	// +optional
	RpsPerPodMax *int32 `json:"rpsPerPodMax,omitempty"`

	// MaxStepSize caps replicas moved per reconcile. nil means "defer to
	// classifier". Operator override per docs/design.md §8.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxStepSize *int32 `json:"maxStepSize,omitempty"`

	// ScaleUpCooldownSeconds. nil means "defer to classifier".
	// +kubebuilder:validation:Minimum=0
	// +optional
	ScaleUpCooldownSeconds *int32 `json:"scaleUpCooldownSeconds,omitempty"`

	// ScaleDownCooldownSeconds. nil means "defer to classifier".
	// +kubebuilder:validation:Minimum=0
	// +optional
	ScaleDownCooldownSeconds *int32 `json:"scaleDownCooldownSeconds,omitempty"`

	// PreferredForecaster. nil or "auto" means "defer to classifier".
	// "gbdt_quantile" is an opt-in spike-aware forecaster; auto/classifier
	// never selects it (F22), so it only ever runs when the user pins it.
	// +kubebuilder:validation:Enum=prophet;linear_extrap;gbdt_quantile;auto
	// +optional
	PreferredForecaster *string `json:"preferredForecaster,omitempty"`
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

// ClassifiedParams holds the classifier's recommended scaling parameters.
// Written exclusively by the ClassifierWorker; reconciler reads but never writes.
// See docs/design.md §6.1 and §7.
type ClassifiedParams struct {
	// Pattern is one of: flat, periodic, spiky, gradual_ramp, default.
	// +kubebuilder:validation:Enum=flat;periodic;spiky;gradual_ramp;default
	Pattern string `json:"pattern"`

	// ScaleUpCooldownSeconds is the classifier's recommended scale-up cooldown.
	ScaleUpCooldownSeconds int32 `json:"scaleUpCooldownSeconds"`

	// ScaleDownCooldownSeconds is the classifier's recommended scale-down cooldown.
	ScaleDownCooldownSeconds int32 `json:"scaleDownCooldownSeconds"`

	// MaxStepSize is the classifier's recommended maximum replica delta per reconcile.
	MaxStepSize int32 `json:"maxStepSize"`

	// PreferredForecaster is "prophet", "linear_extrap", or "gbdt_quantile".
	// Note: per F22 the classifier itself MUST NOT emit "gbdt_quantile" in
	// auto mode — it only appears here when the user pinned it on the spec.
	// +kubebuilder:validation:Enum=prophet;linear_extrap;gbdt_quantile
	PreferredForecaster string `json:"preferredForecaster"`

	// ClassifiedAt is the timestamp of the most recent classification.
	ClassifiedAt metav1.Time `json:"classifiedAt"`

	// HistoryPoints is the count of Prometheus data points used.
	HistoryPoints int32 `json:"historyPoints"`

	// Confidence is "high" if HistoryPoints >= CLASSIFIER_HIGH_CONFIDENCE_POINTS,
	// otherwise "medium". See docs/design.md §4.
	// +kubebuilder:validation:Enum=high;medium
	Confidence string `json:"confidence"`

	// Context carries cold-path-computed scalar features that the hot path
	// forwards verbatim to the Forecast Service. See docs/design_v2.md
	// §6.1 step 6.5 (computation) and §6.3 (forwarding contract).
	// +optional
	Context *ContextFields `json:"context,omitempty"`
}

// ContextFields are the cold-path-computed scalar features (and the 24-bin
// hourly profile) that travel from the classifier worker into the
// AgenticAutoscaler status, then are forwarded verbatim by the controller
// to the Forecast Service /recommend endpoint. The forecasters consume
// these fields to bias predictions when historical context is reliable.
//
// All fields are populated together by the same pipeline run; partial
// population is not supported. The whole object is nil before the first
// successful classification cycle. See docs/design_v2.md §4 and §6.1.
type ContextFields struct {
	// BaselineRPS is the median RPS over the cold-path history window
	// (typically 7 days at 5-min cadence). Integer rps.
	BaselineRPS int32 `json:"baselineRps"`

	// PeakP95RPS is the 95th-percentile RPS over the cold-path history
	// window. Integer rps.
	PeakP95RPS int32 `json:"peakP95Rps"`

	// Trend24hSlope is the 24-hour rolling trend slope, units rps/min.
	// Positive means rising load, negative means falling. See F18.
	Trend24hSlope float64 `json:"trend24hSlope"`

	// HourlyProfile is a 24-bin median-of-RPS-per-UTC-hour profile.
	// Index 0 = 00:00 UTC … index 23 = 23:00 UTC. Bins with insufficient
	// samples are filled by interpolation; HourlyProfileValid records
	// whether the whole profile met the minimum-sample threshold.
	// +kubebuilder:validation:MaxItems=24
	HourlyProfile []int32 `json:"hourlyProfile"`

	// HourlyProfileValid is true iff every UTC-hour bin had at least
	// HOURLY_PROFILE_MIN_HOURS samples in the history window. Forecasters
	// must ignore HourlyProfile when this is false.
	HourlyProfileValid bool `json:"hourlyProfileValid"`
}

// AgenticAutoscalerPhase reflects the controller's high-level state.
// +kubebuilder:validation:Enum=Ready;Disabled;Conflict
type AgenticAutoscalerPhase string

const (
	// PhaseReady indicates normal reconciliation.
	PhaseReady AgenticAutoscalerPhase = "Ready"
	// PhaseDisabled indicates the kill-switch annotation is engaged.
	PhaseDisabled AgenticAutoscalerPhase = "Disabled"
	// PhaseConflict indicates an HPA is also targeting this Deployment.
	PhaseConflict AgenticAutoscalerPhase = "Conflict"
)

// AgenticAutoscalerStatus reports the controller's observed state.
// See docs/design.md §4.
type AgenticAutoscalerStatus struct {
	// Phase is the high-level controller state.
	// +optional
	Phase AgenticAutoscalerPhase `json:"phase,omitempty"`

	// ConflictReason is populated only when Phase=Conflict.
	// +optional
	ConflictReason string `json:"conflictReason,omitempty"`

	// CurrentReplicas is the live replica count of the target Deployment.
	// +optional
	CurrentReplicas int32 `json:"currentReplicas,omitempty"`

	// RecommendedReplicas is the pre-cap, pre-cooldown replica recommendation.
	// See docs/design.md §5 step 5.
	// +optional
	RecommendedReplicas int32 `json:"recommendedReplicas,omitempty"`

	// UnboundedRecommended is the raw forecaster-driven replica count, pre-clamp.
	// When this exceeds Spec.MaxReplicas the CRD bound is the binding constraint;
	// when it is below Spec.MinReplicas the floor is binding. Equals
	// RecommendedReplicas in the common case. See docs/design_v2.md §5 step 5
	// and §6.2 "Field provenance" for the capacity-planning intent.
	// +optional
	UnboundedRecommended int32 `json:"unboundedRecommended,omitempty"`

	// PredictedRPS is the most recent forecast.
	// +optional
	PredictedRPS int32 `json:"predictedRPS,omitempty"`

	// RpsPerPodCurrent is the live sliding-window median rps_per_pod.
	// Persisted for restart recovery (see docs/design.md §5).
	// +optional
	RpsPerPodCurrent int32 `json:"rpsPerPodCurrent,omitempty"`

	// LastScaleTime is the most recent scale event in either direction:
	// max(lastScaleUpTime, lastScaleDownTime).
	// +optional
	LastScaleTime *metav1.Time `json:"lastScaleTime,omitempty"`

	// ClassifiedParams is the classifier's most recent recommendation.
	// +optional
	ClassifiedParams *ClassifiedParams `json:"classifiedParams,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=aas;agentic
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Current",type=integer,JSONPath=`.status.currentReplicas`
// +kubebuilder:printcolumn:name="Recommended",type=integer,JSONPath=`.status.recommendedReplicas`
// +kubebuilder:printcolumn:name="Pattern",type=string,JSONPath=`.status.classifiedParams.pattern`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

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
