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

func ptr32(v int32) *int32  { return &v }
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
