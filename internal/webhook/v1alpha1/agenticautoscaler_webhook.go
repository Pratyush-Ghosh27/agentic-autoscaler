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
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	autoscalingv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/api/v1alpha1"
)

// nolint:unused
// log is for logging in this package.
var agenticautoscalerlog = logf.Log.WithName("agenticautoscaler-resource")

// SetupAgenticAutoscalerWebhookWithManager registers the webhook for AgenticAutoscaler in the manager.
func SetupAgenticAutoscalerWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&autoscalingv1alpha1.AgenticAutoscaler{}).
		WithValidator(&AgenticAutoscalerCustomValidator{}).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-autoscaling-agentic-io-v1alpha1-agenticautoscaler,mutating=false,failurePolicy=fail,sideEffects=None,groups=autoscaling.agentic.io,resources=agenticautoscalers,verbs=create;update,versions=v1alpha1,name=vagenticautoscaler-v1alpha1.kb.io,admissionReviewVersions=v1

// AgenticAutoscalerCustomValidator struct is responsible for validating the AgenticAutoscaler resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type AgenticAutoscalerCustomValidator struct {
	//TODO(user): Add more fields as needed for validation
}

var _ webhook.CustomValidator = &AgenticAutoscalerCustomValidator{}

// ValidateCreate implements webhook.CustomValidator: enforces the design §4
// bound checks on Create.
func (v *AgenticAutoscalerCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	aas, ok := obj.(*autoscalingv1alpha1.AgenticAutoscaler)
	if !ok {
		return nil, fmt.Errorf("expected a AgenticAutoscaler object but got %T", obj)
	}
	agenticautoscalerlog.Info("Validation for AgenticAutoscaler upon creation", "name", aas.GetName())
	return nil, ValidateSpec(&aas.Spec)
}

// ValidateUpdate implements webhook.CustomValidator: re-runs the same bound
// checks on Update so a CR can never be edited into an invalid state.
func (v *AgenticAutoscalerCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	aas, ok := newObj.(*autoscalingv1alpha1.AgenticAutoscaler)
	if !ok {
		return nil, fmt.Errorf("expected a AgenticAutoscaler object for the newObj but got %T", newObj)
	}
	agenticautoscalerlog.Info("Validation for AgenticAutoscaler upon update", "name", aas.GetName())
	return nil, ValidateSpec(&aas.Spec)
}

// ValidateDelete is a no-op: design §4 specifies no delete-time invariants.
func (v *AgenticAutoscalerCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	if aas, ok := obj.(*autoscalingv1alpha1.AgenticAutoscaler); ok {
		agenticautoscalerlog.Info("Validation for AgenticAutoscaler upon deletion", "name", aas.GetName())
	}
	return nil, nil
}
