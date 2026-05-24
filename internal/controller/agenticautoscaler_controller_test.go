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
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/config"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/controller"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/decision"
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
		ForecastHorizon:          10 * time.Minute,
		ForecastTimeout:          5 * time.Second,
		ProphetMinPoints:         60,
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

func ptrInt32(v int32) *int32 { return &v }
