/*
Copyright 2026.
*/

package classifier_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	autoscalingv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/api/v1alpha1"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/prometheus"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/classifier"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/reasoning"
)

// -----------------------------------------------------------------------
// Shared test scaffolding.
// -----------------------------------------------------------------------

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, autoscalingv1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	return s
}

func newSampleCR() *autoscalingv1alpha1.AgenticAutoscaler {
	return &autoscalingv1alpha1.AgenticAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "demo",
			Name:      "app-agentic",
		},
		Spec: autoscalingv1alpha1.AgenticAutoscalerSpec{
			TargetRef: autoscalingv1alpha1.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "app-agentic",
			},
		},
	}
}

// fakeProm is a minimal stand-in for prometheus.Client. Drop a series in,
// optionally an error, and observe how the worker reacts. Concurrency-safe.
type fakeProm struct {
	mu      sync.Mutex
	samples []prometheus.Sample
	err     error
	calls   int
}

func (f *fakeProm) RangeQuery(_ context.Context, _ string, _, _ time.Time, _ time.Duration) ([]prometheus.Sample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.samples, f.err
}

func (f *fakeProm) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeProm) SetSamples(s []prometheus.Sample) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.samples = s
	f.err = nil
}

func (f *fakeProm) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func samplesFromSeries(s []float64) []prometheus.Sample {
	out := make([]prometheus.Sample, len(s))
	base := time.Now().Add(-time.Duration(len(s)) * time.Minute)
	for i, v := range s {
		out[i] = prometheus.Sample{Timestamp: base.Add(time.Duration(i) * time.Minute), Value: v}
	}
	return out
}

func newWorker(t *testing.T, cr *autoscalingv1alpha1.AgenticAutoscaler, prom *fakeProm) (*classifier.Worker, *record.FakeRecorder) {
	t.Helper()
	scheme := newScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(&autoscalingv1alpha1.AgenticAutoscaler{}).
		Build()
	rec := record.NewFakeRecorder(16)
	w := &classifier.Worker{
		Key:            types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name},
		DeploymentName: cr.Spec.TargetRef.Name,
		MinReplicas:    2,
		MaxReplicas:    10,
		Config: classifier.WorkerConfig{
			Interval:       100 * time.Millisecond,
			HistoryHours:   24 * time.Hour,
			MinPoints:      70,
			HighConfPoints: 240,
			DedupSeconds:   60,
		},
		Client:        c,
		Prom:          prom,
		EventRecorder: rec,
		ReclassifyCh:  make(chan struct{}, 1),
		GenerationCh:  make(chan struct{}, 1),
	}
	return w, rec
}

// -----------------------------------------------------------------------
// Pure pipeline integration through the worker package.
// -----------------------------------------------------------------------

func TestWorkerPipelineIntegration(t *testing.T) {
	series := loadSeries(t, "spiky_1440.json")
	result, err := classifier.RunPipeline(series, 240, 70, 2, 10)
	require.NoError(t, err)
	assert.Equal(t, classifier.PatternSpiky, result.Pattern)
	assert.Equal(t, classifier.ConfidenceHigh, result.Confidence)
	assert.Greater(t, result.Params.MaxStep, int32(1))
}

// -----------------------------------------------------------------------
// runClassification happy path: patches status + emits PatternClassified.
// -----------------------------------------------------------------------

func TestWorker_HappyPath_PatchesStatusAndEmitsEvent(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	prom.SetSamples(samplesFromSeries(loadSeries(t, "periodic_1440.json")))
	w, rec := newWorker(t, cr, prom)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Pump the worker once via the goroutine, then cancel.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(ctx)
	}()

	require.Eventually(t, func() bool {
		var got autoscalingv1alpha1.AgenticAutoscaler
		err := w.Client.Get(ctx, w.Key, &got)
		return err == nil && got.Status.ClassifiedParams != nil
	}, 1500*time.Millisecond, 25*time.Millisecond, "classifiedParams never set")

	cancel()
	wg.Wait()

	var got autoscalingv1alpha1.AgenticAutoscaler
	require.NoError(t, w.Client.Get(context.Background(), w.Key, &got))
	require.NotNil(t, got.Status.ClassifiedParams)
	assert.Equal(t, classifier.PatternPeriodic, got.Status.ClassifiedParams.Pattern)
	assert.Equal(t, classifier.ConfidenceHigh, got.Status.ClassifiedParams.Confidence)
	assert.Equal(t, classifier.ForecasterProphet, got.Status.ClassifiedParams.PreferredForecaster)
	assert.False(t, got.Status.ClassifiedParams.ClassifiedAt.IsZero())

	require.Greater(t, prom.Calls(), 0, "Prometheus must be queried")

	// Drain at least one event of the right reason.
	require.Eventually(t, func() bool {
		select {
		case e := <-rec.Events:
			return contains(e, reasoning.PatternClassified)
		default:
			return false
		}
	}, 500*time.Millisecond, 20*time.Millisecond, "expected a PatternClassified event")
}

// -----------------------------------------------------------------------
// Insufficient-points failure path emits PatternUnknown but does not crash.
// -----------------------------------------------------------------------

func TestWorker_InsufficientPoints_EmitsPatternUnknown(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	prom.SetSamples(samplesFromSeries(loadSeries(t, "insufficient_50.json")))
	w, rec := newWorker(t, cr, prom)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(ctx)
	}()

	require.Eventually(t, func() bool {
		select {
		case e := <-rec.Events:
			return contains(e, reasoning.PatternUnknown)
		default:
			return false
		}
	}, 800*time.Millisecond, 20*time.Millisecond)

	cancel()
	wg.Wait()

	var got autoscalingv1alpha1.AgenticAutoscaler
	require.NoError(t, w.Client.Get(context.Background(), w.Key, &got))
	assert.Nil(t, got.Status.ClassifiedParams,
		"insufficient points must not patch classifiedParams")
}

// -----------------------------------------------------------------------
// Prometheus error → no patch, no event, no panic.
// -----------------------------------------------------------------------

func TestWorker_PrometheusError_LogsAndContinues(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	prom.SetError(errors.New("prometheus down"))
	w, _ := newWorker(t, cr, prom)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(ctx)
	}()

	wg.Wait()
	assert.GreaterOrEqual(t, prom.Calls(), 1, "Prometheus should have been called at least once")

	var got autoscalingv1alpha1.AgenticAutoscaler
	require.NoError(t, w.Client.Get(context.Background(), w.Key, &got))
	assert.Nil(t, got.Status.ClassifiedParams)
}

// -----------------------------------------------------------------------
// Reclassify annotation: trigger via channel; success removes the annotation.
// -----------------------------------------------------------------------

func TestWorker_ReclassifyAnnotation_RemovedOnSuccess(t *testing.T) {
	cr := newSampleCR()
	cr.Annotations = map[string]string{
		reasoning.AnnotationReclassify: "true",
	}
	prom := &fakeProm{}
	prom.SetSamples(samplesFromSeries(loadSeries(t, "flat_1440.json")))
	w, _ := newWorker(t, cr, prom)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(ctx)
	}()

	w.ReclassifyCh <- struct{}{}

	require.Eventually(t, func() bool {
		var got autoscalingv1alpha1.AgenticAutoscaler
		if err := w.Client.Get(ctx, w.Key, &got); err != nil {
			return false
		}
		_, present := got.Annotations[reasoning.AnnotationReclassify]
		return !present && got.Status.ClassifiedParams != nil
	}, 1500*time.Millisecond, 25*time.Millisecond,
		"reclassify annotation should have been stripped after a successful classification")

	cancel()
	wg.Wait()
}

// -----------------------------------------------------------------------
// Reclassify annotation: NOT removed when classification fails (so the
// retry will pick it up next iteration).
// -----------------------------------------------------------------------

func TestWorker_ReclassifyAnnotation_KeptOnFailure(t *testing.T) {
	cr := newSampleCR()
	cr.Annotations = map[string]string{
		reasoning.AnnotationReclassify: "true",
	}
	prom := &fakeProm{}
	prom.SetSamples(samplesFromSeries(loadSeries(t, "insufficient_50.json")))
	w, _ := newWorker(t, cr, prom)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(ctx)
	}()

	w.ReclassifyCh <- struct{}{}
	time.Sleep(300 * time.Millisecond)
	cancel()
	wg.Wait()

	var got autoscalingv1alpha1.AgenticAutoscaler
	require.NoError(t, w.Client.Get(context.Background(), w.Key, &got))
	_, present := got.Annotations[reasoning.AnnotationReclassify]
	assert.True(t, present, "annotation must remain when classification did not produce params")
}

// -----------------------------------------------------------------------
// Generation dedup: pure unit on the gating helpers (no goroutine).
// -----------------------------------------------------------------------

func TestWorker_GenerationDedupGating(t *testing.T) {
	w := &classifier.Worker{
		Config: classifier.WorkerConfig{DedupSeconds: 60},
	}
	w.SetNowForTest(func() time.Time { return time.Unix(1_000_000, 0) })

	w.SetLastClassifyAt(time.Unix(1_000_000-30, 0))
	assert.False(t, w.ShouldRunOnGeneration(), "30s ago is within 60s dedup window")

	w.SetLastClassifyAt(time.Unix(1_000_000-90, 0))
	assert.True(t, w.ShouldRunOnGeneration(), "90s ago is outside 60s dedup window")
}

// -----------------------------------------------------------------------
// Context cancellation exits the loop promptly.
// -----------------------------------------------------------------------

func TestWorker_ContextCancellationExits(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	prom.SetSamples(samplesFromSeries(loadSeries(t, "flat_1440.json")))
	w, _ := newWorker(t, cr, prom)

	ctx, cancel := context.WithCancel(context.Background())

	var done atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(ctx)
		done.Store(true)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	require.Eventually(t, func() bool { return done.Load() },
		500*time.Millisecond, 20*time.Millisecond,
		"worker should exit promptly on ctx cancel")
	wg.Wait()
}

// -----------------------------------------------------------------------
// Drop-and-replace channel mechanics — sanity check on the buffer.
// -----------------------------------------------------------------------

func TestWorker_ReclassifySignalDropAndReplace(t *testing.T) {
	ch := make(chan struct{}, 1)
	ch <- struct{}{}
	// drain
	select {
	case <-ch:
	default:
	}
	// re-fill
	select {
	case ch <- struct{}{}:
	default:
		t.Fatal("expected re-fill to succeed")
	}
	<-ch
	select {
	case <-ch:
		t.Fatal("expected empty channel")
	default:
	}
}

// -----------------------------------------------------------------------
// Panic recovery: a panicking Prom should not bring down the worker; the
// recover defer sleeps for PanicBackoff, then the loop restarts. We verify
// the worker survives by cancelling ctx during the backoff.
// -----------------------------------------------------------------------

type panickyProm struct{ calls atomic.Int32 }

func (p *panickyProm) RangeQuery(_ context.Context, _ string, _, _ time.Time, _ time.Duration) ([]prometheus.Sample, error) {
	p.calls.Add(1)
	panic("synthetic panic")
}

func TestWorker_PanicRecovery_DoesNotCrashGoroutine(t *testing.T) {
	prevBackoff := classifier.PanicBackoff
	classifier.PanicBackoff = 50 * time.Millisecond
	t.Cleanup(func() { classifier.PanicBackoff = prevBackoff })

	cr := newSampleCR()
	prom := &panickyProm{}
	scheme := newScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(&autoscalingv1alpha1.AgenticAutoscaler{}).
		Build()
	w := &classifier.Worker{
		Key:            types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name},
		DeploymentName: cr.Spec.TargetRef.Name,
		MinReplicas:    2,
		MaxReplicas:    10,
		Config: classifier.WorkerConfig{
			Interval:       50 * time.Millisecond,
			HistoryHours:   24 * time.Hour,
			MinPoints:      70,
			HighConfPoints: 240,
			DedupSeconds:   60,
		},
		Client:        c,
		Prom:          prom,
		EventRecorder: record.NewFakeRecorder(4),
		ReclassifyCh:  make(chan struct{}, 1),
		GenerationCh:  make(chan struct{}, 1),
	}

	ctx, cancel := context.WithCancel(context.Background())
	var done atomic.Bool
	go func() {
		w.Run(ctx)
		done.Store(true)
	}()

	// Wait for at least one panic to be observed.
	require.Eventually(t, func() bool { return prom.calls.Load() >= 1 },
		500*time.Millisecond, 10*time.Millisecond)

	cancel()
	require.Eventually(t, func() bool { return done.Load() },
		2*time.Second, 25*time.Millisecond,
		"worker must exit even while in PanicBackoff sleep")
}

// -----------------------------------------------------------------------
// Periodic timer fires repeatedly: drive the worker for a few intervals
// and observe multiple Prometheus calls. Covers the `case <-timer.C`
// branch of runOnce.
// -----------------------------------------------------------------------

func TestWorker_PeriodicTimerFires(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	prom.SetSamples(samplesFromSeries(loadSeries(t, "flat_1440.json")))
	w, _ := newWorker(t, cr, prom)
	w.Config.Interval = 30 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)

	require.Eventually(t, func() bool { return prom.Calls() >= 3 },
		800*time.Millisecond, 10*time.Millisecond,
		"expected at least 3 Prometheus calls (immediate + 2 timer ticks)")

	cancel()
	time.Sleep(50 * time.Millisecond)
}

// -----------------------------------------------------------------------
// patchStatus failure: delete the CR after the worker starts; subsequent
// classification cycles must log + continue, not panic. Exercises the
// patchStatus Get-error branch.
// -----------------------------------------------------------------------

func TestWorker_PatchFailsWhenCRDeleted(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	prom.SetSamples(samplesFromSeries(loadSeries(t, "flat_1440.json")))
	w, _ := newWorker(t, cr, prom)
	w.Config.Interval = 25 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)

	// Wait for the first classification then delete the CR.
	require.Eventually(t, func() bool {
		var got autoscalingv1alpha1.AgenticAutoscaler
		err := w.Client.Get(ctx, w.Key, &got)
		return err == nil && got.Status.ClassifiedParams != nil
	}, 1500*time.Millisecond, 25*time.Millisecond)

	require.NoError(t, w.Client.Delete(ctx, cr))

	// Worker must keep ticking despite Get/patch failures.
	beforeCalls := prom.Calls()
	require.Eventually(t, func() bool { return prom.Calls() > beforeCalls },
		800*time.Millisecond, 10*time.Millisecond,
		"worker should keep polling after CR delete")

	cancel()
	time.Sleep(50 * time.Millisecond)
}

// -----------------------------------------------------------------------
// LastClassifyAt accessor.
// -----------------------------------------------------------------------

func TestWorker_LastClassifyAtAccessor(t *testing.T) {
	w := &classifier.Worker{}
	assert.True(t, w.LastClassifyAt().IsZero())
	t0 := time.Unix(1_700_000_000, 0)
	w.SetLastClassifyAt(t0)
	assert.Equal(t, t0, w.LastClassifyAt())
}

// -----------------------------------------------------------------------
// trendSlope short-circuit for n<2 — exercise the empty/single-point branch.
// -----------------------------------------------------------------------

func TestExtractFeatures_SinglePointSeries(t *testing.T) {
	f := classifier.ExtractFeatures([]float64{42.0})
	assert.Equal(t, 0.0, f.TrendSlope, "n=1 → no slope")
}

func TestExtractFeatures_AllZeroSeries(t *testing.T) {
	// All-zero series: mean=0 → cv=0 (mean<1 guard); slope=0; tod=0.
	f := classifier.ExtractFeatures(make([]float64, 100))
	assert.Equal(t, 0.0, f.CV)
	assert.Equal(t, 0.0, f.TrendSlope)
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
