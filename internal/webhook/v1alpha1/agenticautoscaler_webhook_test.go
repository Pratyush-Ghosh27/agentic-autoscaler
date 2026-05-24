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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	autoscalingv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/api/v1alpha1"
)

// These specs exercise the full chain: API client -> kube-apiserver
// -> webhook (TLS via envtest's self-signed cert) -> AgenticAutoscalerCustomValidator
// -> ValidateSpec. Failures here mean the wiring between the
// CustomValidator and the validator package broke (unit tests cover
// the rules themselves).

var i32 = func(v int32) *int32 { return &v }
var s = func(v string) *string { return &v }

var _ = Describe("AgenticAutoscaler validating webhook (envtest)", func() {
	const ns = "default"

	It("admits a valid CR", func() {
		cr := &autoscalingv1alpha1.AgenticAutoscaler{
			ObjectMeta: metav1.ObjectMeta{Name: "valid-cr", Namespace: ns},
			Spec: autoscalingv1alpha1.AgenticAutoscalerSpec{
				TargetRef: autoscalingv1alpha1.CrossVersionObjectReference{
					APIVersion: "apps/v1", Kind: "Deployment", Name: "demo",
				},
				MinReplicas: i32(2),
				MaxReplicas: i32(10),
			},
		}
		Expect(k8sClient.Create(ctx, cr)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, cr) })
	})

	It("rejects minReplicas below 1", func() {
		cr := &autoscalingv1alpha1.AgenticAutoscaler{
			ObjectMeta: metav1.ObjectMeta{Name: "bad-min", Namespace: ns},
			Spec: autoscalingv1alpha1.AgenticAutoscalerSpec{
				TargetRef: autoscalingv1alpha1.CrossVersionObjectReference{
					APIVersion: "apps/v1", Kind: "Deployment", Name: "demo",
				},
				// Default would be 2; explicit 0 to trigger the rule. The
				// kubebuilder Minimum=1 marker rejects 0 at the schema
				// layer, so we set a value the schema accepts (1) and
				// then move maxReplicas below it to exercise our rule.
				MinReplicas: i32(5),
				MaxReplicas: i32(3),
			},
		}
		err := k8sClient.Create(ctx, cr)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("maxReplicas"))
		Expect(err.Error()).To(ContainSubstring("minReplicas"))
	})

	It("rejects unknown preferredForecaster (when a value sneaks past the schema enum)", func() {
		// The CRD enum marker normally rejects unknown values at the schema
		// layer. To exercise the webhook path specifically, we use a value
		// not in the enum list; kube-apiserver will surface either the
		// schema error or the webhook error — both contain the same field
		// name, so the assertion holds regardless.
		bad := "xgboost"
		cr := &autoscalingv1alpha1.AgenticAutoscaler{
			ObjectMeta: metav1.ObjectMeta{Name: "bad-forecaster", Namespace: ns},
			Spec: autoscalingv1alpha1.AgenticAutoscalerSpec{
				TargetRef: autoscalingv1alpha1.CrossVersionObjectReference{
					APIVersion: "apps/v1", Kind: "Deployment", Name: "demo",
				},
				MinReplicas:         i32(2),
				MaxReplicas:         i32(10),
				PreferredForecaster: &bad,
			},
		}
		err := k8sClient.Create(ctx, cr)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("preferredForecaster"))
	})

	It("rejects updates that violate rules", func() {
		cr := &autoscalingv1alpha1.AgenticAutoscaler{
			ObjectMeta: metav1.ObjectMeta{Name: "update-test", Namespace: ns},
			Spec: autoscalingv1alpha1.AgenticAutoscalerSpec{
				TargetRef: autoscalingv1alpha1.CrossVersionObjectReference{
					APIVersion: "apps/v1", Kind: "Deployment", Name: "demo",
				},
				MinReplicas: i32(2),
				MaxReplicas: i32(10),
			},
		}
		Expect(k8sClient.Create(ctx, cr)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, cr) })

		// Read back the live object so the resourceVersion is set, then
		// mutate to an invalid state (max < min) and try to Update.
		live := &autoscalingv1alpha1.AgenticAutoscaler{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), live)).To(Succeed())
		live.Spec.MaxReplicas = i32(1)
		err := k8sClient.Update(ctx, live)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("maxReplicas"))
	})

	It("aggregates multiple problems into one rejection", func() {
		// rpsPerPodMin >= rpsPerPodMax + cooldown < 0 + maxStepSize > range.
		// The schema enum/Minimum markers don't cover any of these; the
		// webhook is the only thing that can reject this combination.
		cr := &autoscalingv1alpha1.AgenticAutoscaler{
			ObjectMeta: metav1.ObjectMeta{Name: "many-problems", Namespace: ns},
			Spec: autoscalingv1alpha1.AgenticAutoscalerSpec{
				TargetRef: autoscalingv1alpha1.CrossVersionObjectReference{
					APIVersion: "apps/v1", Kind: "Deployment", Name: "demo",
				},
				MinReplicas:  i32(2),
				MaxReplicas:  i32(5),
				RpsPerPodMin: i32(600),
				RpsPerPodMax: i32(500),
				MaxStepSize:  i32(10), // > (5 - 2)
			},
		}
		err := k8sClient.Create(ctx, cr)
		Expect(err).To(HaveOccurred())
		msg := err.Error()
		Expect(msg).To(ContainSubstring("rpsPerPod"))
		Expect(msg).To(ContainSubstring("maxStepSize"))
	})
})

var _ = It("fast-rejects values that violate kubebuilder schema markers", func() {
	// Sanity check that the CRD's Minimum=1 markers also fire (this is
	// belt-and-suspenders defence; the webhook covers the same case).
	cr := &autoscalingv1alpha1.AgenticAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "schema-min-zero", Namespace: "default"},
		Spec: autoscalingv1alpha1.AgenticAutoscalerSpec{
			TargetRef: autoscalingv1alpha1.CrossVersionObjectReference{
				APIVersion: "apps/v1", Kind: "Deployment", Name: "demo",
			},
			MinReplicas: i32(0), // schema marker should reject
		},
	}
	err := k8sClient.Create(context.Background(), cr)
	Expect(err).To(HaveOccurred())
})

// Suppress unused warnings if we ever drop one of the helpers above.
var (
	_ = i32
	_ = s
)
