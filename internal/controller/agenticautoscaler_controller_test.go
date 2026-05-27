/*
Copyright 2026.
*/

package controller_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	autoscalingv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/api/v1alpha1"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/forecast"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/prometheus"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/classifier"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/config"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/controller"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/decision"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/reasoning"
)

// -----------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------

// fixedNow is the wall-clock anchor for envtest reconciles. Cooldown maths
// resolve against this value, so tests are deterministic regardless of
// when the suite actually runs.
var fixedNow = time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)

func testConfig() *config.Config {
	return &config.Config{
		ForecastServiceURL:       "http://forecast:8000",
		PrometheusURL:            "http://prom:9090",
		ReconcileInterval:        60 * time.Second,
		HotPathHistory:           60 * time.Minute,
		HotPathMinPoints:         10,
		ForecastTimeout:          5 * time.Second,
		ProphetMinPoints:         30,
		DefaultScaleUpCooldown:   60 * time.Second,
		DefaultScaleDownCooldown: 300 * time.Second,
		DefaultMaxStepSize:       4,
	}
}

// rangeSamples returns n synthetic samples each with the given value.
func rangeSamples(n int, val float64) []prometheus.Sample {
	out := make([]prometheus.Sample, n)
	for i := 0; i < n; i++ {
		out[i] = prometheus.Sample{Timestamp: fixedNow.Add(time.Duration(-i) * time.Minute), Value: val}
	}
	return out
}

// newReconciler returns a reconciler configured with k8sClient and the
// supplied fakes. Tests get an intentionally-fresh StateStore so cooldowns
// reset between runs.
func newReconciler(prom *fakePromQuerier, fc *fakeForecaster, ex *fakeExplainNotifier) *controller.AgenticAutoscalerReconciler {
	scheme := newScheme()
	return &controller.AgenticAutoscalerReconciler{
		Client:        k8sClient,
		Scheme:        scheme,
		EventRecorder: &record.FakeRecorder{Events: make(chan string, 16)},
		Config:        testConfig(),
		PromQuerier:   prom,
		Forecaster:    fc,
		ExplainNotify: ex,
		StateStore:    decision.NewStateStore(),
		Now:           func() time.Time { return fixedNow },
	}
}

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	Expect(scheme.AddToScheme(s)).To(Succeed())
	Expect(autoscalingv1alpha1.AddToScheme(s)).To(Succeed())
	return s
}

// makeDeployment creates a Deployment with the given replicas in the given
// namespace and returns it (already persisted via k8sClient).
func makeDeployment(ctx context.Context, namespace, name string, replicas int32) *appsv1.Deployment {
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "nginx:1.27"}},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, d)).To(Succeed())
	return d
}

// makeAAS creates a minimal valid AgenticAutoscaler.
func makeAAS(ctx context.Context, namespace, name, deploymentName string, mods ...func(*autoscalingv1alpha1.AgenticAutoscaler)) *autoscalingv1alpha1.AgenticAutoscaler {
	min := int32(2)
	max := int32(10)
	rmin := int32(50)
	rmax := int32(500)
	aas := &autoscalingv1alpha1.AgenticAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: autoscalingv1alpha1.AgenticAutoscalerSpec{
			TargetRef: autoscalingv1alpha1.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       deploymentName,
			},
			MinReplicas:  &min,
			MaxReplicas:  &max,
			RpsPerPodMin: &rmin,
			RpsPerPodMax: &rmax,
		},
	}
	for _, m := range mods {
		m(aas)
	}
	Expect(k8sClient.Create(ctx, aas)).To(Succeed())
	return aas
}

func ensureNamespace(ctx context.Context, name string) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := k8sClient.Create(ctx, ns); err != nil && !errors.Is(err, ctx.Err()) {
		// AlreadyExists is fine — namespaces persist across specs in envtest.
		// Anything else means real trouble.
		Expect(client.IgnoreAlreadyExists(err)).To(Succeed())
	}
}

// reconcileFor invokes a single reconcile against the named CR.
func reconcileFor(ctx context.Context, r *controller.AgenticAutoscalerReconciler, ns, name string) (ctrl.Result, error) {
	return r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: name}})
}

// fetch gets the live CR.
func fetch(ctx context.Context, ns, name string) *autoscalingv1alpha1.AgenticAutoscaler {
	var out autoscalingv1alpha1.AgenticAutoscaler
	Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &out)).To(Succeed())
	return &out
}

// fetchDeploy gets the live Deployment.
func fetchDeploy(ctx context.Context, ns, name string) *appsv1.Deployment {
	var out appsv1.Deployment
	Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &out)).To(Succeed())
	return &out
}

// -----------------------------------------------------------------------
// Specs
// -----------------------------------------------------------------------

var _ = Describe("AgenticAutoscalerReconciler pre-checks", func() {
	const ns = "rec-prechecks"
	ctx := context.Background()

	BeforeEach(func() {
		ensureNamespace(ctx, ns)
	})

	It("sets phase=Disabled when kill-switch annotation is present", func() {
		const dep = "ks-deploy"
		const cr = "ks-cr"
		makeDeployment(ctx, ns, dep, 2)
		makeAAS(ctx, ns, cr, dep, func(a *autoscalingv1alpha1.AgenticAutoscaler) {
			a.Annotations = map[string]string{"autoscaling.agentic.io/kill-switch": "true"}
		})
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		prom := &fakePromQuerier{}
		fc := &fakeForecaster{}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)

		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		Expect(string(fetch(ctx, ns, cr).Status.Phase)).To(Equal(string(autoscalingv1alpha1.PhaseDisabled)))
		Expect(fc.lastRequest()).To(BeNil(), "kill switch must short-circuit before forecast")
		Expect(ex.callCount()).To(Equal(0), "no ExplainNotify on disabled CR")
	})

	It("transitions to Conflict when an HPA targets the same Deployment", func() {
		const dep = "hpa-deploy"
		const cr = "hpa-cr"
		makeDeployment(ctx, ns, dep, 3)
		makeAAS(ctx, ns, cr, dep)

		hpa := &autoscalingv2.HorizontalPodAutoscaler{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "rival"},
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       dep,
				},
				MinReplicas: ptrInt32(1),
				MaxReplicas: 5,
			},
		}
		Expect(k8sClient.Create(ctx, hpa)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, hpa)
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		prom := &fakePromQuerier{}
		fc := &fakeForecaster{}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)

		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		got := fetch(ctx, ns, cr)
		Expect(string(got.Status.Phase)).To(Equal(string(autoscalingv1alpha1.PhaseConflict)))
		Expect(got.Status.ConflictReason).To(ContainSubstring("rival"))
		Expect(fc.lastRequest()).To(BeNil(), "conflict must short-circuit before forecast")
	})

	It("clears Conflict back to Ready after the rival HPA is removed", func() {
		const dep = "hpa-clear-deploy"
		const cr = "hpa-clear-cr"
		makeDeployment(ctx, ns, dep, 3)
		makeAAS(ctx, ns, cr, dep)
		hpa := &autoscalingv2.HorizontalPodAutoscaler{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "rival-clear"},
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					APIVersion: "apps/v1", Kind: "Deployment", Name: dep,
				},
				MinReplicas: ptrInt32(1),
				MaxReplicas: 5,
			},
		}
		Expect(k8sClient.Create(ctx, hpa)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(20, 500)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 1000, ModelUsed: "linear_extrap"}}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)

		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(fetch(ctx, ns, cr).Status.Phase)).To(Equal(string(autoscalingv1alpha1.PhaseConflict)))

		Expect(k8sClient.Delete(ctx, hpa)).To(Succeed())

		_, err = reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		got := fetch(ctx, ns, cr)
		Expect(string(got.Status.Phase)).To(Equal(string(autoscalingv1alpha1.PhaseReady)))
		Expect(got.Status.ConflictReason).To(BeEmpty())
	})
})

var _ = Describe("AgenticAutoscalerReconciler hot path", func() {
	const ns = "rec-hot"
	ctx := context.Background()

	BeforeEach(func() {
		ensureNamespace(ctx, ns)
	})

	It("scales up, writes status, and notifies the ExplainWorker", func() {
		const dep = "happy-deploy"
		const cr = "happy-cr"
		makeDeployment(ctx, ns, dep, 2)
		makeAAS(ctx, ns, cr, dep)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		// rps_per_pod default seeds to midpoint = (50+500)/2 = 275.
		// recommended = ceil(1000 / 275) = 4. current=2, MaxStep=4 → target=4.
		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(20, 500)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 1000, ModelUsed: "linear_extrap"}}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)

		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		got := fetch(ctx, ns, cr)
		Expect(string(got.Status.Phase)).To(Equal(string(autoscalingv1alpha1.PhaseReady)))
		Expect(got.Status.RecommendedReplicas).To(Equal(int32(4)))
		Expect(got.Status.CurrentReplicas).To(Equal(int32(4)))
		Expect(got.Status.PredictedRPS).To(Equal(int32(1000)))
		Expect(got.Status.LastScaleTime).NotTo(BeNil())

		deploy := fetchDeploy(ctx, ns, dep)
		Expect(*deploy.Spec.Replicas).To(Equal(int32(4)))

		Expect(ex.callCount()).To(Equal(1))
		Expect(ex.last().Reason).To(Equal("scale_up"))
		Expect(ex.last().TargetReplicas).To(Equal(int32(4)))

		// Wire equivalence: "auto" (default) is normalised to "" before POST.
		Expect(fc.lastRequest()).NotTo(BeNil())
		Expect(fc.lastRequest().PreferredModel).To(BeEmpty())
		Expect(fc.lastRequest().WorkloadID).To(Equal(fmt.Sprintf("%s/%s", ns, cr)))
	})

	It("records pre-cap recommendedReplicas when maxStepSize clips the patch", func() {
		const dep = "cap-deploy"
		const cr = "cap-cr"
		makeDeployment(ctx, ns, dep, 2)
		// MaxStepSize=2 caps the patch to current+2=4 even though the pure
		// recommendation is much higher.
		two := int32(2)
		makeAAS(ctx, ns, cr, dep, func(a *autoscalingv1alpha1.AgenticAutoscaler) {
			a.Spec.MaxStepSize = &two
		})
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		// predicted=2750 / rps_per_pod=275 -> recommended=10 (clamped to maxReplicas=10)
		// step cap clips to current(2)+2 = 4.
		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(20, 500)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 2750, ModelUsed: "linear_extrap"}}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)

		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		got := fetch(ctx, ns, cr)
		Expect(got.Status.RecommendedReplicas).To(Equal(int32(10)),
			"recommendedReplicas is the pre-cap value")
		Expect(got.Status.CurrentReplicas).To(Equal(int32(4)),
			"actual replicas obey the cap")
		Expect(*fetchDeploy(ctx, ns, dep).Spec.Replicas).To(Equal(int32(4)))
		Expect(ex.last().Reason).To(Equal("step_capped_up"))
	})

	It("respects an operator's preferred forecaster on the wire", func() {
		const dep = "pref-deploy"
		const cr = "pref-cr"
		makeDeployment(ctx, ns, dep, 2)
		preferred := "prophet"
		makeAAS(ctx, ns, cr, dep, func(a *autoscalingv1alpha1.AgenticAutoscaler) {
			a.Spec.PreferredForecaster = &preferred
		})
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(20, 500)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 800, ModelUsed: "prophet"}}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)

		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())
		Expect(fc.lastRequest().PreferredModel).To(Equal("prophet"))
	})
})

var _ = Describe("AgenticAutoscalerReconciler failure paths", func() {
	const ns = "rec-fail"
	ctx := context.Background()

	BeforeEach(func() { ensureNamespace(ctx, ns) })

	It("emits metrics_unavailable when the instant query returns an error", func() {
		const dep = "fail-instant-deploy"
		const cr = "fail-instant-cr"
		makeDeployment(ctx, ns, dep, 2)
		makeAAS(ctx, ns, cr, dep)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		prom := &fakePromQuerier{instantErr: errors.New("connection refused")}
		fc := &fakeForecaster{}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)
		fakeRec, ok := r.EventRecorder.(*record.FakeRecorder)
		Expect(ok).To(BeTrue())

		res, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", time.Duration(0)))

		Expect(*fetchDeploy(ctx, ns, dep).Spec.Replicas).To(Equal(int32(2)),
			"no replica change on metrics failure")
		Expect(fc.lastRequest()).To(BeNil(), "forecast not called on metrics failure")
		Eventually(fakeRec.Events).Should(Receive(ContainSubstring("metrics_unavailable")))
	})

	It("emits metrics_unavailable when range history is below HOT_PATH_MIN_POINTS", func() {
		const dep = "fail-short-deploy"
		const cr = "fail-short-cr"
		makeDeployment(ctx, ns, dep, 2)
		makeAAS(ctx, ns, cr, dep)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(5, 500)}
		fc := &fakeForecaster{}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)
		fakeRec := r.EventRecorder.(*record.FakeRecorder)

		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())
		Expect(fc.lastRequest()).To(BeNil())
		Eventually(fakeRec.Events).Should(Receive(ContainSubstring("metrics_unavailable")))
	})

	It("emits forecast_unavailable when the forecast service errors", func() {
		const dep = "fail-fc-deploy"
		const cr = "fail-fc-cr"
		makeDeployment(ctx, ns, dep, 2)
		makeAAS(ctx, ns, cr, dep)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(20, 500)}
		fc := &fakeForecaster{err: errors.New("forecast timeout")}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)
		fakeRec := r.EventRecorder.(*record.FakeRecorder)

		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())
		Expect(*fetchDeploy(ctx, ns, dep).Spec.Replicas).To(Equal(int32(2)),
			"no replica change on forecast failure")
		Eventually(fakeRec.Events).Should(Receive(ContainSubstring("forecast_unavailable")))
	})

	It("requeues without status update when the target Deployment is missing", func() {
		const dep = "missing-deploy"
		const cr = "missing-cr"
		makeAAS(ctx, ns, cr, dep)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
		})

		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(20, 500)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 1000, ModelUsed: "linear_extrap"}}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)

		res, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", time.Duration(0)))
	})
})

// -----------------------------------------------------------------------
// Cold-restart cooldown recovery — design §5 step 5.
//
// On controller pod restart, the in-memory StateStore is empty but the
// CR's status still carries the last scale time from before the
// restart. Without the persisted-status seed, every restart would
// immediately allow another scale (because state.LastScaleUpTime is
// the zero value, "much longer than the cooldown ago"). The reconciler
// calls decision.InitializeFromStatus to seed both LastScaleUp and
// LastScaleDown times from aas.Status.LastScaleTime; this spec proves
// the wiring end-to-end through a real reconcile.
// -----------------------------------------------------------------------
var _ = Describe("AgenticAutoscalerReconciler cold-restart cooldown", func() {
	const ns = "rec-coldstart"
	ctx := context.Background()

	BeforeEach(func() { ensureNamespace(ctx, ns) })

	It("honours scale-down cooldown after a cold restart (status-seeded)", func() {
		const dep = "cold-down-deploy"
		const cr = "cold-down-cr"
		// 4 replicas — overprovisioned relative to the about-to-arrive
		// forecast, so the *unguarded* path would scale down to
		// minReplicas=2 immediately.
		makeDeployment(ctx, ns, dep, 4)
		makeAAS(ctx, ns, cr, dep)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		// Persist a "we just scaled 30 s ago" status. The default scale-down
		// cooldown from testConfig is 300 s, so 30 s < 300 s ⇒ gated.
		got := fetch(ctx, ns, cr)
		t := metav1.NewTime(fixedNow.Add(-30 * time.Second))
		got.Status.LastScaleTime = &t
		got.Status.CurrentReplicas = 4
		got.Status.RpsPerPodCurrent = 275
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

		// Predict near-zero RPS so the unguarded recommendation collapses
		// to minReplicas. instantVal also low so the rps_per_pod ring isn't
		// dragged toward an unrealistic value.
		prom := &fakePromQuerier{instantVal: 50, rangeVal: rangeSamples(20, 50)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 50, ModelUsed: "linear_extrap"}}
		ex := &fakeExplainNotifier{}
		// Brand-new reconciler ⇒ brand-new (empty) StateStore. This is
		// the "cold restart" — the only thing protecting the deployment
		// from an immediate scale-down is the persisted LastScaleTime.
		r := newReconciler(prom, fc, ex)
		fakeRec := r.EventRecorder.(*record.FakeRecorder)

		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		// The Deployment must NOT have been touched.
		Expect(*fetchDeploy(ctx, ns, dep).Spec.Replicas).To(Equal(int32(4)),
			"cold-restart cooldown should have blocked the scale-down")

		// CurrentReplicas in status reflects what we actually have now,
		// which is still 4 — the cooldown gate set Target=Current.
		Expect(fetch(ctx, ns, cr).Status.CurrentReplicas).To(Equal(int32(4)))

		// And the controller should have surfaced the cooldown event.
		Eventually(fakeRec.Events).Should(Receive(ContainSubstring("cooldown_holding_down")))
	})

	It("permits scale-up when the persisted LastScaleTime is older than the up-cooldown", func() {
		const dep = "cold-up-deploy"
		const cr = "cold-up-cr"
		makeDeployment(ctx, ns, dep, 2)
		makeAAS(ctx, ns, cr, dep)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		// LastScaleTime 10 minutes ago — well past the 60 s up-cooldown.
		// This is the "cold restart, status carries an old timestamp"
		// case: the seed must NOT cause a false cooldown gate.
		got := fetch(ctx, ns, cr)
		t := metav1.NewTime(fixedNow.Add(-10 * time.Minute))
		got.Status.LastScaleTime = &t
		got.Status.CurrentReplicas = 2
		got.Status.RpsPerPodCurrent = 275
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

		// On the first reconcile the rps_per_pod ring buffer hasn't been
		// seeded yet, so the steady-state gate fires and pushes its first
		// observation = currentRPS / currentReplicas = 500 / 2 = 250.
		// recommended = ceil(predictedRPS / rps_per_pod) = ceil(1100 / 250) = 5.
		// Step cap MaxStep=4 doesn't bind (|5-2|=3 ≤ 4), so target=5.
		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(20, 500)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 1100, ModelUsed: "linear_extrap"}}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)
		fakeRec := r.EventRecorder.(*record.FakeRecorder)

		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		Expect(*fetchDeploy(ctx, ns, dep).Spec.Replicas).To(Equal(int32(5)),
			"old LastScaleTime must not falsely gate fresh scale-up decisions")
		Eventually(fakeRec.Events).Should(Receive(ContainSubstring("scale_up")))
	})

	It("forwards classifier context to the Forecast Service when present (G10)", func() {
		const dep = "ctx-fwd-deploy"
		const cr = "ctx-fwd-cr"
		makeDeployment(ctx, ns, dep, 2)
		makeAAS(ctx, ns, cr, dep)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		// Pre-populate status.classifiedParams.context so the reconciler
		// has something to forward. Status is a sub-resource so we
		// fetch, mutate, and Update via Status().
		got := fetch(ctx, ns, cr)
		hourly := make([]int32, 24)
		for i := range hourly {
			hourly[i] = int32(50 + i)
		}
		got.Status.ClassifiedParams = &autoscalingv1alpha1.ClassifiedParams{
			Pattern:                  "periodic",
			ScaleUpCooldownSeconds:   60,
			ScaleDownCooldownSeconds: 180,
			MaxStepSize:              4,
			PreferredForecaster:      "prophet",
			Confidence:               "high",
			HistoryPoints:            288,
			ClassifiedAt:             metav1.Time{Time: time.Now().UTC()},
			Context: &autoscalingv1alpha1.ContextFields{
				BaselineRPS:        100,
				PeakP95RPS:         500,
				Trend24hSlope:      0.25,
				HourlyProfile:      hourly,
				HourlyProfileValid: true,
			},
		}
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

		// Sanity: confirm the status was actually persisted before we
		// reconcile. Without this assertion a stale-Get bug would
		// look like "controller doesn't forward Context" — same RED
		// failure mode for two very different root causes.
		Eventually(func() *autoscalingv1alpha1.ContextFields {
			refetched := fetch(ctx, ns, cr)
			if refetched.Status.ClassifiedParams == nil {
				return nil
			}
			return refetched.Status.ClassifiedParams.Context
		}, "2s", "50ms").ShouldNot(BeNil(),
			"status.classifiedParams.context must be persisted before reconcile")

		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(20, 500)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 1000, ModelUsed: "prophet"}}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)

		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		req := fc.lastRequest()
		Expect(req).NotTo(BeNil())
		Expect(req.Context).NotTo(BeNil(), "Context must be non-nil when status has it")
		Expect(req.Context.BaselineRPS).To(Equal(int32(100)))
		Expect(req.Context.PeakP95RPS).To(Equal(int32(500)))
		Expect(req.Context.Trend24hSlope).To(BeNumerically("~", 0.25, 0.0001))
		Expect(req.Context.HourlyProfile).To(HaveLen(24))
		Expect(req.Context.HourlyProfileValid).To(BeTrue())
		// CurrentHourUTC and CurrentMinuteUTC are the controller's clock.
		Expect(req.Context.CurrentHourUTC).To(BeNumerically(">=", int32(0)))
		Expect(req.Context.CurrentHourUTC).To(BeNumerically("<=", int32(23)))
		Expect(req.Context.CurrentMinuteUTC).To(BeNumerically(">=", int32(0)))
		Expect(req.Context.CurrentMinuteUTC).To(BeNumerically("<=", int32(59)))
	})

	It("omits Context when status has no classifiedParams (cold start, G10)", func() {
		const dep = "ctx-cold-deploy"
		const cr = "ctx-cold-cr"
		makeDeployment(ctx, ns, dep, 2)
		makeAAS(ctx, ns, cr, dep)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(20, 500)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 1000, ModelUsed: "linear_extrap"}}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)

		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		req := fc.lastRequest()
		Expect(req).NotTo(BeNil())
		Expect(req.Context).To(BeNil(),
			"cold start (no classifiedParams) ⇒ Context must be nil so JSON omits the field")
	})

	It("respects the skip-context annotation and sends nil Context (G10)", func() {
		const dep = "ctx-skip-deploy"
		const cr = "ctx-skip-cr"
		makeDeployment(ctx, ns, dep, 2)
		makeAAS(ctx, ns, cr, dep, func(a *autoscalingv1alpha1.AgenticAutoscaler) {
			a.Annotations = map[string]string{
				"autoscaling.agentic.io/skip-context": "true",
			}
		})
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		// Status has Context, but the annotation should override.
		got := fetch(ctx, ns, cr)
		got.Status.ClassifiedParams = &autoscalingv1alpha1.ClassifiedParams{
			Pattern:                  "periodic",
			ScaleUpCooldownSeconds:   60,
			ScaleDownCooldownSeconds: 180,
			MaxStepSize:              4,
			PreferredForecaster:      "prophet",
			Confidence:               "high",
			HistoryPoints:            288,
			ClassifiedAt:             metav1.Time{Time: time.Now().UTC()},
			Context: &autoscalingv1alpha1.ContextFields{
				BaselineRPS: 100, PeakP95RPS: 200,
				HourlyProfile: make([]int32, 24), HourlyProfileValid: true,
			},
		}
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(20, 500)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 1000, ModelUsed: "linear_extrap"}}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)

		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		req := fc.lastRequest()
		Expect(req).NotTo(BeNil())
		Expect(req.Context).To(BeNil(),
			"skip-context annotation must override the status context block")
	})
})

// -----------------------------------------------------------------------
// Reclassify annotation end-to-end — design §6.1 trigger 3.
//
// The cold-path classifier is wired in production via the
// reconciler -> classifier.Manager -> Worker chain (G1). This spec
// proves the full reclassify flow in envtest: an operator sets
// `autoscaling.agentic.io/reclassify=true` on a CR; the reconciler
// signals the manager; the worker classifies; the worker strips the
// annotation. Before G1, the worker was never started, so the
// annotation accumulated on every CR for which an operator pressed
// the button — the controller silently ignored it.
// -----------------------------------------------------------------------
var _ = Describe("AgenticAutoscalerReconciler reclassify annotation", func() {
	const ns = "rec-reclassify"
	ctx := context.Background()

	BeforeEach(func() { ensureNamespace(ctx, ns) })

	It("strips the reclassify annotation after a successful classification", func() {
		const dep = "reclass-deploy"
		const cr = "reclass-cr"
		makeDeployment(ctx, ns, dep, 2)
		makeAAS(ctx, ns, cr, dep, func(a *autoscalingv1alpha1.AgenticAutoscaler) {
			a.Annotations = map[string]string{
				"autoscaling.agentic.io/reclassify": "true",
			}
		})
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		// Long-lived context for the worker goroutine. Cancelled at the
		// end of the spec so the goroutine doesn't leak into the next
		// test in the suite.
		workerCtx, cancelWorker := context.WithCancel(context.Background())
		DeferCleanup(cancelWorker)

		// 80 samples ≥ MinPoints (70). All flat — the pipeline classifies
		// as `flat`, which is a *successful* classification (the annotation
		// removal path requires success, not a specific pattern).
		prom := &fakePromQuerier{
			instantVal: 100,
			rangeVal:   rangeSamples(80, 100),
		}
		mgr := classifier.NewManager(
			workerCtx,
			k8sClient,
			prom,
			&record.FakeRecorder{Events: make(chan string, 32)},
			classifier.WorkerConfig{
				// Long enough that the timer never fires during this spec
				// — we exercise reclassify, not the periodic path.
				Interval:       time.Hour,
				HistoryHours:   24 * time.Hour,
				MinPoints:      70,
				HighConfPoints: 240,
				DedupSeconds:   1,
			},
		)

		fetched := fetch(ctx, ns, cr)
		mgr.Ensure(fetched)

		// Push the reclassify trigger. Drop-and-replace; the worker
		// picks it up on its next select and runs runClassification,
		// which calls removeReclassifyAnnotation on success.
		mgr.SignalReclassify(types.NamespacedName{Namespace: ns, Name: cr})

		// Eventually the annotation should be gone from the live CR.
		// Long timeout because the worker's first thing is the immediate
		// initial run, then it loops; on slow CI workers this can take
		// a couple of seconds.
		Eventually(func() bool {
			got := fetch(ctx, ns, cr)
			_, present := got.Annotations["autoscaling.agentic.io/reclassify"]
			return !present
		}, "10s", "100ms").Should(BeTrue(),
			"reclassify annotation must be removed after the worker classifies")

		// And status.classifiedParams must be populated — proving the
		// classification *actually ran* rather than the annotation being
		// stripped by some other code path.
		Eventually(func() *autoscalingv1alpha1.ClassifiedParams {
			return fetch(ctx, ns, cr).Status.ClassifiedParams
		}, "10s", "100ms").ShouldNot(BeNil(),
			"status.classifiedParams must be populated by the worker run")
	})

	It("does not strip the annotation when classification fails on insufficient history", func() {
		const dep = "reclass-fail-deploy"
		const cr = "reclass-fail-cr"
		makeDeployment(ctx, ns, dep, 2)
		makeAAS(ctx, ns, cr, dep, func(a *autoscalingv1alpha1.AgenticAutoscaler) {
			a.Annotations = map[string]string{
				"autoscaling.agentic.io/reclassify": "true",
			}
		})
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		workerCtx, cancelWorker := context.WithCancel(context.Background())
		DeferCleanup(cancelWorker)

		// Only 5 samples — well below MinPoints=70. RunPipeline will
		// return an "insufficient history" error and the worker will
		// emit pattern_unknown without stripping the annotation.
		prom := &fakePromQuerier{
			instantVal: 100,
			rangeVal:   rangeSamples(5, 100),
		}
		mgr := classifier.NewManager(
			workerCtx,
			k8sClient,
			prom,
			&record.FakeRecorder{Events: make(chan string, 32)},
			classifier.WorkerConfig{
				Interval:       time.Hour,
				HistoryHours:   24 * time.Hour,
				MinPoints:      70,
				HighConfPoints: 240,
				DedupSeconds:   1,
			},
		)

		fetched := fetch(ctx, ns, cr)
		mgr.Ensure(fetched)
		mgr.SignalReclassify(types.NamespacedName{Namespace: ns, Name: cr})

		// Annotation must persist — the operator's request hasn't been
		// satisfied yet. Consistently across repeated polls, not just
		// "eventually true" (which a removal would also satisfy).
		Consistently(func() bool {
			got := fetch(ctx, ns, cr)
			_, present := got.Annotations["autoscaling.agentic.io/reclassify"]
			return present
		}, "1500ms", "100ms").Should(BeTrue(),
			"failed classification must NOT strip the annotation; the operator's "+
				"reclassify request is still outstanding")
	})
})

func ptrInt32(v int32) *int32 { return &v }

// -----------------------------------------------------------------------
// G16 — Revision-annotation watcher (Plan 16 T3, F19)
// -----------------------------------------------------------------------

var _ = Describe("AgenticAutoscalerReconciler G16 revision watcher", func() {
	const ns = "rec-g16-revision"
	ctx := context.Background()

	BeforeEach(func() {
		ensureNamespace(ctx, ns)
	})

	It("reads deployment.kubernetes.io/revision annotation, not metadata.generation", func() {
		const dep = "rev-deploy"
		const cr = "rev-cr"
		// Create the Deployment with the revision annotation pre-set,
		// since envtest doesn't run the Deployment controller that would
		// normally bump it.
		makeDeployment(ctx, ns, dep, 2)
		// Set the revision annotation explicitly (envtest has no Deployment
		// controller to set it for us).
		d := fetchDeploy(ctx, ns, dep)
		if d.Annotations == nil {
			d.Annotations = map[string]string{}
		}
		d.Annotations["deployment.kubernetes.io/revision"] = "1"
		Expect(k8sClient.Update(ctx, d)).To(Succeed())

		makeAAS(ctx, ns, cr, dep)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		workerCtx, cancelWorker := context.WithCancel(context.Background())
		DeferCleanup(cancelWorker)

		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(20, 500)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 1000, ModelUsed: "linear_extrap"}}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)

		// Real Manager wired into the reconciler. We don't care if the
		// worker actually classifies — we observe whether the reconciler
		// passed the revision annotation (string "1") into the manager,
		// not the deployment.Generation (int64 monotonic).
		mgr := classifier.NewManager(
			workerCtx,
			k8sClient,
			prom,
			&record.FakeRecorder{Events: make(chan string, 32)},
			classifier.WorkerConfig{
				Interval:       time.Hour,
				HistoryHours:   24 * time.Hour,
				MinPoints:      70,
				HighConfPoints: 240,
				DedupSeconds:   60,
			},
		)
		r.Classifier = mgr

		// First reconcile — Ensure() registers a worker and the
		// reconciler calls ObserveDeploymentRevision(key, "1").
		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		key := types.NamespacedName{Namespace: ns, Name: cr}
		Expect(mgr.LastDeploymentRevision(key)).To(Equal("1"),
			"reconciler must read .Annotations[\"deployment.kubernetes.io/revision\"]")
	})

	It("does NOT update last-observed revision when /scale bumps generation but annotation is unchanged", func() {
		const dep = "rev-stable-deploy"
		const cr = "rev-stable-cr"
		makeDeployment(ctx, ns, dep, 2)
		d := fetchDeploy(ctx, ns, dep)
		if d.Annotations == nil {
			d.Annotations = map[string]string{}
		}
		d.Annotations["deployment.kubernetes.io/revision"] = "5"
		Expect(k8sClient.Update(ctx, d)).To(Succeed())

		makeAAS(ctx, ns, cr, dep)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		workerCtx, cancelWorker := context.WithCancel(context.Background())
		DeferCleanup(cancelWorker)

		// Configure the forecaster to push the workload from 2 → 6 replicas
		// so the reconciler issues a /scale patch (which bumps generation).
		prom := &fakePromQuerier{instantVal: 600, rangeVal: rangeSamples(20, 600)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 1500, ModelUsed: "linear_extrap"}}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)

		mgr := classifier.NewManager(
			workerCtx,
			k8sClient,
			prom,
			&record.FakeRecorder{Events: make(chan string, 32)},
			classifier.WorkerConfig{
				Interval:       time.Hour,
				HistoryHours:   24 * time.Hour,
				MinPoints:      70,
				HighConfPoints: 240,
				DedupSeconds:   60,
			},
		)
		r.Classifier = mgr

		key := types.NamespacedName{Namespace: ns, Name: cr}

		// First reconcile — issues a /scale patch (generation bumps) but
		// revision annotation stays "5".
		genBefore := fetchDeploy(ctx, ns, dep).Generation
		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())
		genAfter := fetchDeploy(ctx, ns, dep).Generation

		// Sanity-check the test premise: the reconciler must have actually
		// bumped generation via /scale. If it didn't (e.g. cooldown blocked
		// the scale), we can't assert the desired property.
		if genBefore == genAfter {
			Skip("envtest did not bump generation on /scale; test premise invalid")
		}

		// First-observation seed.
		Expect(mgr.LastDeploymentRevision(key)).To(Equal("5"))

		// Second reconcile — generation bumped again on the second /scale
		// patch (or stays the same if no scale fires). Either way, the
		// revision annotation is still "5", so LastDeploymentRevision
		// must still be "5".
		_, err = reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())
		Expect(fetchDeploy(ctx, ns, dep).Annotations["deployment.kubernetes.io/revision"]).
			To(Equal("5"), "/scale must not change the revision annotation")
		Expect(mgr.LastDeploymentRevision(key)).To(Equal("5"),
			"a /scale patch must NOT update the manager's revision tracker")
	})

	It("updates last-observed revision when the rollout annotation actually changes", func() {
		const dep = "rev-rollout-deploy"
		const cr = "rev-rollout-cr"
		makeDeployment(ctx, ns, dep, 2)
		d := fetchDeploy(ctx, ns, dep)
		if d.Annotations == nil {
			d.Annotations = map[string]string{}
		}
		d.Annotations["deployment.kubernetes.io/revision"] = "1"
		Expect(k8sClient.Update(ctx, d)).To(Succeed())

		makeAAS(ctx, ns, cr, dep)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		workerCtx, cancelWorker := context.WithCancel(context.Background())
		DeferCleanup(cancelWorker)

		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(20, 500)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 600, ModelUsed: "linear_extrap"}}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)

		mgr := classifier.NewManager(
			workerCtx,
			k8sClient,
			prom,
			&record.FakeRecorder{Events: make(chan string, 32)},
			classifier.WorkerConfig{
				Interval:       time.Hour,
				HistoryHours:   24 * time.Hour,
				MinPoints:      70,
				HighConfPoints: 240,
				DedupSeconds:   1,
			},
		)
		r.Classifier = mgr

		key := types.NamespacedName{Namespace: ns, Name: cr}

		// First reconcile seeds revision="1".
		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())
		Expect(mgr.LastDeploymentRevision(key)).To(Equal("1"))

		// Simulate a rollout: bump the revision annotation to "2".
		d = fetchDeploy(ctx, ns, dep)
		d.Annotations["deployment.kubernetes.io/revision"] = "2"
		Expect(k8sClient.Update(ctx, d)).To(Succeed())

		// Second reconcile reads the new revision and updates the tracker.
		_, err = reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())
		Expect(mgr.LastDeploymentRevision(key)).To(Equal("2"),
			"rollout (revision annotation change) must update the tracker")
	})
})

// -----------------------------------------------------------------------
// G13 — UnboundedRecommended + MaxReplicasBinding end-to-end (Plan 15 T14)
// -----------------------------------------------------------------------

var _ = Describe("AgenticAutoscalerReconciler G13 binding surfacing", func() {
	const ns = "rec-g13-binding"
	ctx := context.Background()

	BeforeEach(func() {
		ensureNamespace(ctx, ns)
	})

	It("persists UnboundedRecommended into status and emits MaxReplicasBinding event when forecast exceeds maxReplicas", func() {
		const dep = "bind-deploy"
		const cr = "bind-cr"
		// Deployment is *already at* maxReplicas (10), so the workload sits
		// at the cap and the forecast can't push it any higher. This is the
		// canonical "binding without replica change" scenario from
		// design_v2.md §5 precedence rule 4.
		makeDeployment(ctx, ns, dep, 10)
		makeAAS(ctx, ns, cr, dep)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		// First-reconcile rps_per_pod math: gate fires (no prior scale time),
		// observation = currentRPS/currentReplicas = 500/10 = 50. Ring-buffer
		// median = 50 → ClampRpsPerPod(50, 50, 500) = 50.
		// unbounded = ceil(4000 / 50) = 80 (>> maxReplicas=10).
		// Expected: unboundedRecommended=80, recommendedReplicas=10,
		// target=10, no patch, reason=MaxReplicasBinding.
		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(20, 500)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 4000, ModelUsed: "linear_extrap"}}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)
		fakeRec := r.EventRecorder.(*record.FakeRecorder)

		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		got := fetch(ctx, ns, cr)
		Expect(got.Status.UnboundedRecommended).To(Equal(int32(80)),
			"status.unboundedRecommended is the raw forecaster ask, pre-clamp")
		Expect(got.Status.RecommendedReplicas).To(Equal(int32(10)),
			"status.recommendedReplicas is post-clamp (= maxReplicas)")
		Expect(got.Status.CurrentReplicas).To(Equal(int32(10)),
			"workload stays at the cap; no replica change")

		// Deployment replica count must not change.
		Expect(*fetchDeploy(ctx, ns, dep).Spec.Replicas).To(Equal(int32(10)))

		// ExplainWorker must NOT be notified per design_v2.md §6.2 line 885
		// ("Binding without replica change exclusion").
		Expect(ex.callCount()).To(Equal(0),
			"max_replicas_binding without replica change must not trigger ExplainWorker")

		// K8s Event still fires for diagnostic visibility, with the
		// MaxReplicasBinding reason and unboundedRecommended=80 in the message.
		Eventually(fakeRec.Events).Should(Receive(
			SatisfyAll(
				ContainSubstring(reasoning.MaxReplicasBinding),
				ContainSubstring("unboundedRecommended=80"),
				ContainSubstring("recommended=10"),
			)))
	})

	It("includes unboundedRecommended in event message when step cap also fires", func() {
		const dep = "bind-stepcap-deploy"
		const cr = "bind-stepcap-cr"
		// Deployment at 2, maxReplicas=10, maxStep=2 — step cap will clip
		// the move. Binding reason gets overwritten by step_capped_up per
		// design §5 precedence rule 2, but unboundedRecommended is still
		// in the event message because unbounded != recommended.
		makeDeployment(ctx, ns, dep, 2)
		two := int32(2)
		makeAAS(ctx, ns, cr, dep, func(a *autoscalingv1alpha1.AgenticAutoscaler) {
			a.Spec.MaxStepSize = &two
		})
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		// First-reconcile rps_per_pod math: observation = 500/2 = 250.
		// ClampRpsPerPod(250, 50, 500) = 250.
		// unbounded = ceil(4000 / 250) = 16 (> maxReplicas=10).
		// Recommended clamped to 10; step cap clips target to current(2)+maxStep(2)=4.
		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(20, 500)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 4000, ModelUsed: "linear_extrap"}}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)
		fakeRec := r.EventRecorder.(*record.FakeRecorder)

		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		got := fetch(ctx, ns, cr)
		Expect(got.Status.UnboundedRecommended).To(Equal(int32(16)))
		Expect(got.Status.RecommendedReplicas).To(Equal(int32(10)))
		Expect(*fetchDeploy(ctx, ns, dep).Spec.Replicas).To(Equal(int32(4)),
			"step cap clips the patch to current+maxStep=4")

		// ExplainWorker IS notified — replicas changed.
		Expect(ex.callCount()).To(Equal(1))
		Expect(ex.last().Reason).To(Equal(reasoning.StepCappedUp),
			"step 6 cap overwrites binding reason when target moves")
		Expect(ex.last().UnboundedRecommended).To(Equal(int32(16)),
			"ExplainRequest carries the unbounded value for the prompt's capacity-planning prose")

		Eventually(fakeRec.Events).Should(Receive(
			SatisfyAll(
				ContainSubstring(reasoning.StepCappedUp),
				ContainSubstring("unboundedRecommended=16"),
			)))
	})

	It("omits unboundedRecommended from event message when it equals recommended", func() {
		const dep = "no-bind-deploy"
		const cr = "no-bind-cr"
		makeDeployment(ctx, ns, dep, 2)
		makeAAS(ctx, ns, cr, dep)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		// predicted=1000 / 275 = 4 — well inside [2, 10]; no binding.
		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(20, 500)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 1000, ModelUsed: "linear_extrap"}}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)
		fakeRec := r.EventRecorder.(*record.FakeRecorder)

		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		got := fetch(ctx, ns, cr)
		Expect(got.Status.UnboundedRecommended).To(Equal(int32(4)))
		Expect(got.Status.RecommendedReplicas).To(Equal(int32(4)))

		// Event message must NOT include "unboundedRecommended=" when it
		// equals recommended (the common case — keeps events short).
		Eventually(fakeRec.Events).Should(Receive(
			SatisfyAll(
				ContainSubstring(reasoning.ScaleUp),
				Not(ContainSubstring("unboundedRecommended=")),
				Not(ContainSubstring("recommended=4")),
			)))
	})
})
