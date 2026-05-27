/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	autoscalingv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/api/v1alpha1"
	webhookv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/internal/webhook/v1alpha1"
)

func ptr32(v int32) *int32    { return &v }
func ptrStr(s string) *string { return &s }

// validCR returns a minimally valid AgenticAutoscaler for use as a base
// in negative-rule tests; each test mutates exactly the field under test.
func validCR() *autoscalingv1alpha1.AgenticAutoscaler {
	return &autoscalingv1alpha1.AgenticAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app-agentic",
			Namespace: "demo",
		},
		Spec: autoscalingv1alpha1.AgenticAutoscalerSpec{
			TargetRef: autoscalingv1alpha1.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "app-agentic",
			},
			MinReplicas:  ptr32(2),
			MaxReplicas:  ptr32(10),
			RpsPerPodMin: ptr32(50),
			RpsPerPodMax: ptr32(500),
		},
	}
}

func TestValidateSpec_HappyPath(t *testing.T) {
	cr := validCR()
	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.NoError(t, err)
	_ = assert.NotNil // satisfy import for later tests
}

func TestValidateSpec_RejectsMinReplicasZero(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(0)

	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "minReplicas")
}

func TestValidateSpec_RejectsMinReplicasNegative(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(-1)

	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "minReplicas")
}

func TestValidateSpec_RejectsMaxReplicasBelowMin(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(5)
	cr.Spec.MaxReplicas = ptr32(3)

	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maxReplicas")
	assert.Contains(t, err.Error(), "minReplicas")
}

// TestValidateSpec_RejectsMinEqualsMax pins F37: maxReplicas == minReplicas
// is rejected at admission. The §7 maxStep formula has an empty clamp
// range when min == max (lower 1, upper 0), making any maxStep
// classification a degenerate clamp. Operators who genuinely want a
// pinned replica count should set min and max one apart and rely on the
// scaler's hysteresis to keep the deployment at min.
func TestValidateSpec_RejectsMinEqualsMax(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(5)
	cr.Spec.MaxReplicas = ptr32(5)

	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.Error(t, err, "min == max must be rejected per F37 strict inequality")
	assert.Contains(t, err.Error(), "maxReplicas")
	assert.Contains(t, err.Error(), "minReplicas")
}

func TestValidateSpec_AcceptsMaxOneAboveMin(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(5)
	cr.Spec.MaxReplicas = ptr32(6)

	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.NoError(t, err, "max = min+1 is the smallest valid range")
}

func TestValidateSpec_RejectsRpsPerPodMinZero(t *testing.T) {
	cr := validCR()
	cr.Spec.RpsPerPodMin = ptr32(0)
	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rpsPerPodMin")
}

func TestValidateSpec_RejectsRpsPerPodMinAboveMax(t *testing.T) {
	cr := validCR()
	cr.Spec.RpsPerPodMin = ptr32(500)
	cr.Spec.RpsPerPodMax = ptr32(500)
	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rpsPerPodMin")
	assert.Contains(t, err.Error(), "rpsPerPodMax")
}

func TestValidateSpec_AcceptsRpsPerPodMinJustBelowMax(t *testing.T) {
	cr := validCR()
	cr.Spec.RpsPerPodMin = ptr32(499)
	cr.Spec.RpsPerPodMax = ptr32(500)
	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.NoError(t, err)
}

func TestValidateSpec_AcceptsMaxStepSizeNil(t *testing.T) {
	cr := validCR()
	cr.Spec.MaxStepSize = nil
	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.NoError(t, err)
}

func TestValidateSpec_RejectsMaxStepSizeZero(t *testing.T) {
	cr := validCR()
	cr.Spec.MaxStepSize = ptr32(0)
	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maxStepSize")
}

func TestValidateSpec_RejectsMaxStepSizeAboveRange(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(2)
	cr.Spec.MaxReplicas = ptr32(5)
	cr.Spec.MaxStepSize = ptr32(4) // 4 > (5 - 2)
	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maxStepSize")
	assert.Contains(t, err.Error(), "maxReplicas - minReplicas")
}

func TestValidateSpec_AcceptsMaxStepSizeAtRange(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(2)
	cr.Spec.MaxReplicas = ptr32(5)
	cr.Spec.MaxStepSize = ptr32(3) // exactly (5 - 2)
	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.NoError(t, err)
}

func TestValidateSpec_AcceptsCooldownsNil(t *testing.T) {
	cr := validCR()
	cr.Spec.ScaleUpCooldownSeconds = nil
	cr.Spec.ScaleDownCooldownSeconds = nil
	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.NoError(t, err)
}

func TestValidateSpec_AcceptsCooldownsZero(t *testing.T) {
	cr := validCR()
	cr.Spec.ScaleUpCooldownSeconds = ptr32(0)
	cr.Spec.ScaleDownCooldownSeconds = ptr32(0)
	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.NoError(t, err)
}

func TestValidateSpec_RejectsNegativeScaleUpCooldown(t *testing.T) {
	cr := validCR()
	cr.Spec.ScaleUpCooldownSeconds = ptr32(-5)
	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scaleUpCooldownSeconds")
}

func TestValidateSpec_RejectsNegativeScaleDownCooldown(t *testing.T) {
	cr := validCR()
	cr.Spec.ScaleDownCooldownSeconds = ptr32(-1)
	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scaleDownCooldownSeconds")
}

func TestValidateSpec_AcceptsKnownForecasters(t *testing.T) {
	for _, model := range []string{"prophet", "linear_extrap", "gbdt_quantile", "auto"} {
		t.Run(model, func(t *testing.T) {
			cr := validCR()
			cr.Spec.PreferredForecaster = ptrStr(model)
			err := webhookv1alpha1.ValidateSpec(&cr.Spec)
			require.NoError(t, err)
		})
	}
}

func TestValidateSpec_RejectsUnknownForecaster(t *testing.T) {
	cr := validCR()
	cr.Spec.PreferredForecaster = ptrStr("xgboost")
	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "preferredForecaster")
	assert.Contains(t, err.Error(), "xgboost")
}

func TestValidateSpec_AcceptsNilForecaster(t *testing.T) {
	cr := validCR()
	cr.Spec.PreferredForecaster = nil
	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.NoError(t, err)
}

func TestValidateSpec_AggregatesMultipleProblems(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(0) // problem 1
	cr.Spec.RpsPerPodMin = ptr32(600)
	cr.Spec.RpsPerPodMax = ptr32(500)           // problem 2 (min >= max)
	cr.Spec.PreferredForecaster = ptrStr("foo") // problem 3

	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "minReplicas")
	assert.Contains(t, msg, "rpsPerPod")
	assert.Contains(t, msg, "preferredForecaster")
}
