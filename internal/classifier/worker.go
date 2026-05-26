/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package classifier

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	autoscalingv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/api/v1alpha1"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/prometheus"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/promql"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/reasoning"
)

// PromQuerier is the subset of *prometheus.Client (Plan #3) the worker uses.
// Defined here (not in the controller package) so the classifier package
// compiles without importing controller.
type PromQuerier interface {
	RangeQuery(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]prometheus.Sample, error)
}

// PanicBackoff is how long the worker sleeps after a recovered panic
// before attempting another classification cycle. Long enough that a
// crash-loop on a single CR doesn't spin the CPU. Exposed as a var so
// tests can shrink it without redefining the worker loop.
var PanicBackoff = 60 * time.Second

// WorkerConfig is the env-var-driven slice the worker depends on.
// Built once by main.go from internal/config and passed to every worker.
type WorkerConfig struct {
	Interval       time.Duration
	HistoryHours   time.Duration
	MinPoints      int
	HighConfPoints int
	DedupSeconds   int

	// ResolutionMin is the cold-path PromQL step in minutes (default
	// in v2: 5). When zero or negative the worker falls back to a
	// 1-minute step and the legacy RunPipeline so existing v1 callers
	// keep their behaviour.
	ResolutionMin int
	// HourlyProfileMinHours is the minimum number of distinct UTC
	// hours that must be populated for ContextOutput.HourlyProfileValid
	// to be true. Default 12. G11.
	HourlyProfileMinHours int
	// CVGuardMeanRPS is the per-deployment override for the mean-RPS
	// floor below which CV is forced to 0 and which clamps the
	// peakToTrough denominator. F29 + F32c.
	CVGuardMeanRPS float64
}

// Worker is the cold-path classification goroutine for one
// AgenticAutoscaler CR. The reconciler spawns one Worker per CR via
// `SetupWithManager` (wired in Plan #11) and cancels it on delete.
type Worker struct {
	// Identity & target.
	Key            types.NamespacedName
	DeploymentName string
	MinReplicas    int32
	MaxReplicas    int32

	// Dependencies (interfaces so tests substitute fakes).
	Config        WorkerConfig
	Client        client.Client
	Prom          PromQuerier
	EventRecorder record.EventRecorder

	// Signal channels. Buffered, capacity 1, drop-and-replace semantics.
	ReclassifyCh chan struct{}
	GenerationCh chan struct{}

	// now is overridable in tests; nil → time.Now.
	now func() time.Time

	lastClassifyAt time.Time
}

// Now returns the worker's clock. Use everywhere internally so tests can
// inject a fixed time without flake.
func (w *Worker) Now() time.Time {
	if w.now == nil {
		return time.Now()
	}
	return w.now()
}

// SetNowForTest overrides the worker's clock for unit tests. The
// `_ForTest` suffix keeps the public surface honest about who should
// call it.
func (w *Worker) SetNowForTest(fn func() time.Time) { w.now = fn }

// SetLastClassifyAt sets the worker's last-classified timestamp. Exported
// for unit tests of the dedup logic.
func (w *Worker) SetLastClassifyAt(t time.Time) { w.lastClassifyAt = t }

// LastClassifyAt returns the timestamp of the most recent successful
// classification. Exported for tests and the controller's status updater.
func (w *Worker) LastClassifyAt() time.Time { return w.lastClassifyAt }

// ShouldRunOnGeneration reports whether a generation-change signal
// should be honoured given the dedup window. The reconciler pushes onto
// GenerationCh on every CR generation change; this method gates the
// actual classification call so a flurry of unrelated spec edits doesn't
// thrash the cold path.
func (w *Worker) ShouldRunOnGeneration() bool {
	dedup := time.Duration(w.Config.DedupSeconds) * time.Second
	return w.Now().Sub(w.lastClassifyAt) > dedup
}

// Run starts the worker loop. Blocks until ctx is cancelled.
//
// The loop is restart-safe: on panic, the recover defers a PanicBackoff
// sleep and re-enters the loop. This is design §9's "ClassifierWorker
// goroutine panics → recover + 60s backoff".
func (w *Worker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !w.runOnce(ctx) {
			return
		}
	}
}

// runOnce runs a single iteration of the loop, isolated by a panic-
// recovery boundary. Returns false when ctx is cancelled.
func (w *Worker) runOnce(ctx context.Context) (keepGoing bool) {
	defer func() {
		if r := recover(); r != nil {
			logger := log.FromContext(ctx).WithValues("worker", "classifier", "cr", w.Key)
			logger.Error(fmt.Errorf("panic: %v", r), "ClassifierWorker panicked, backing off")
			select {
			case <-ctx.Done():
				keepGoing = false
			case <-time.After(PanicBackoff):
				keepGoing = true
			}
		}
	}()

	logger := log.FromContext(ctx).WithValues("worker", "classifier", "cr", w.Key)

	// Trigger 1: immediate first run on entry.
	w.runClassification(ctx, logger)

	timer := time.NewTicker(w.Config.Interval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			w.runClassification(ctx, logger)
		case <-w.ReclassifyCh:
			if w.runClassification(ctx, logger) {
				w.removeReclassifyAnnotation(ctx, logger)
			}
		case <-w.GenerationCh:
			if w.ShouldRunOnGeneration() {
				w.runClassification(ctx, logger)
			} else {
				logger.V(1).Info("generation signal deduped",
					"sinceLast", w.Now().Sub(w.lastClassifyAt))
			}
		}
	}
}

// runClassification performs one full pipeline: range-query Prometheus →
// run the classification pipeline → patch status → emit Event. Returns
// true on success, false on any failure path.
//
// All failure modes (Prometheus error, insufficient points, CR not found,
// patch failure) log and emit a degraded-state event but never panic, so
// the worker keeps running.
//
// When WorkerConfig.ResolutionMin > 0 the worker queries Prometheus at
// that step (default v2: 5m) and runs RunPipelineV2 so the cold-path
// context block is computed and patched onto status. Otherwise it
// preserves v1 semantics: a 1-minute step and RunPipeline (no context).
func (w *Worker) runClassification(ctx context.Context, logger logr.Logger) bool {
	end := w.Now()
	start := end.Add(-w.Config.HistoryHours)

	step := time.Duration(w.Config.ResolutionMin) * time.Minute
	if step <= 0 {
		step = time.Minute
	}

	samples, err := w.Prom.RangeQuery(ctx,
		promql.RangeRPS(w.DeploymentName), start, end, step)
	if err != nil {
		logger.Error(err, "prometheus range query failed")
		return false
	}

	series := make([]float64, len(samples))
	for i, s := range samples {
		series[i] = s.Value
	}

	if w.Config.ResolutionMin > 0 {
		cfg := PipelineConfig{
			ResolutionMin:         w.Config.ResolutionMin,
			HourlyProfileMinHours: w.Config.HourlyProfileMinHours,
			CVGuardMeanRPS:        w.Config.CVGuardMeanRPS,
			StartHourUTC:          start.UTC().Hour(),
		}
		v2Result, v2Err := RunPipelineV2(series,
			w.Config.HighConfPoints, w.Config.MinPoints,
			w.MinReplicas, w.MaxReplicas, cfg)
		if v2Err != nil {
			w.emitEvent(ctx, reasoning.PatternUnknown,
				fmt.Sprintf("insufficient history: %d points (need %d)",
					len(series), w.Config.MinPoints))
			return false
		}
		if patchErr := w.patchStatusV2(ctx, v2Result); patchErr != nil {
			logger.Error(patchErr, "failed to patch classifiedParams")
			return false
		}
		w.lastClassifyAt = w.Now()
		w.emitEvent(ctx, reasoning.PatternClassified,
			formatClassifiedMessage(v2Result.PipelineResult))
		return true
	}

	result, err := RunPipeline(series,
		w.Config.HighConfPoints, w.Config.MinPoints,
		w.MinReplicas, w.MaxReplicas)
	if err != nil {
		// Insufficient points — a *normal* steady state (just-deployed
		// targets, brief outages). Log + Event, continue.
		w.emitEvent(ctx, reasoning.PatternUnknown,
			fmt.Sprintf("insufficient history: %d points (need %d)",
				len(series), w.Config.MinPoints))
		return false
	}

	if err := w.patchStatus(ctx, result); err != nil {
		logger.Error(err, "failed to patch classifiedParams")
		return false
	}

	w.lastClassifyAt = w.Now()

	w.emitEvent(ctx, reasoning.PatternClassified,
		formatClassifiedMessage(result))
	return true
}

// patchStatus writes result to status.classifiedParams via a
// status-subresource update. We use the standard Get → mutate → Update
// pattern; on conflict the next reconcile/timer will retry.
func (w *Worker) patchStatus(ctx context.Context, result PipelineResult) error {
	var aas autoscalingv1alpha1.AgenticAutoscaler
	if err := w.Client.Get(ctx, w.Key, &aas); err != nil {
		return fmt.Errorf("get CR: %w", err)
	}

	aas.Status.ClassifiedParams = &autoscalingv1alpha1.ClassifiedParams{
		Pattern:                  result.Pattern,
		ScaleUpCooldownSeconds:   result.Params.ScaleUpCooldown,
		ScaleDownCooldownSeconds: result.Params.ScaleDownCooldown,
		MaxStepSize:              result.Params.MaxStep,
		PreferredForecaster:      result.Params.PreferredForecaster,
		ClassifiedAt:             metav1.NewTime(w.Now()),
		HistoryPoints:            historyPointsAsInt32(result.HistoryPoints),
		Confidence:               result.Confidence,
	}

	return w.Client.Status().Update(ctx, &aas)
}

// patchStatusV2 mirrors patchStatus but additionally writes the
// cold-path-computed Context block. The hot-path reconciler will read
// status.classifiedParams.context and forward it verbatim to the
// Forecast Service in T14. G10 + G11.
func (w *Worker) patchStatusV2(ctx context.Context, result PipelineResultV2) error {
	var aas autoscalingv1alpha1.AgenticAutoscaler
	if err := w.Client.Get(ctx, w.Key, &aas); err != nil {
		return fmt.Errorf("get CR: %w", err)
	}

	cp := &autoscalingv1alpha1.ClassifiedParams{
		Pattern:                  result.Pattern,
		ScaleUpCooldownSeconds:   result.Params.ScaleUpCooldown,
		ScaleDownCooldownSeconds: result.Params.ScaleDownCooldown,
		MaxStepSize:              result.Params.MaxStep,
		PreferredForecaster:      result.Params.PreferredForecaster,
		ClassifiedAt:             metav1.NewTime(w.Now()),
		HistoryPoints:            historyPointsAsInt32(result.HistoryPoints),
		Confidence:               result.Confidence,
	}
	if result.Context != nil {
		cp.Context = &autoscalingv1alpha1.ContextFields{
			BaselineRPS:        result.Context.BaselineRPS,
			PeakP95RPS:         result.Context.PeakP95RPS,
			Trend24hSlope:      result.Context.Trend24hSlope,
			HourlyProfile:      result.Context.HourlyProfile,
			HourlyProfileValid: result.Context.HourlyProfileValid,
		}
	}
	aas.Status.ClassifiedParams = cp

	return w.Client.Status().Update(ctx, &aas)
}

// removeReclassifyAnnotation strips the reclassify annotation after a
// successful classification. Failure to strip is logged but not fatal —
// a follow-up reconcile/timer will eventually re-classify and retry.
func (w *Worker) removeReclassifyAnnotation(ctx context.Context, logger logr.Logger) {
	var aas autoscalingv1alpha1.AgenticAutoscaler
	if err := w.Client.Get(ctx, w.Key, &aas); err != nil {
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "failed to fetch CR to remove reclassify annotation")
		}
		return
	}
	if aas.Annotations == nil {
		return
	}
	if _, present := aas.Annotations[reasoning.AnnotationReclassify]; !present {
		return
	}
	delete(aas.Annotations, reasoning.AnnotationReclassify)
	if err := w.Client.Update(ctx, &aas); err != nil {
		logger.Error(err, "failed to remove reclassify annotation")
	}
}

// emitEvent looks up the CR (so the EventRecorder has an involvedObject)
// and records the event. If the CR cannot be fetched we silently skip —
// a missing CR means we're racing a delete; nothing to do.
//
// Event type is always Normal (Warning-class transitions are surfaced
// via controller logs + metrics, not via Events, to keep the apiserver
// event budget bounded on noisy clusters). If a future caller needs a
// Warning event, reintroduce the parameter then.
func (w *Worker) emitEvent(ctx context.Context, reason, message string) {
	if w.EventRecorder == nil {
		return
	}
	var aas autoscalingv1alpha1.AgenticAutoscaler
	if err := w.Client.Get(ctx, w.Key, &aas); err != nil {
		return
	}
	w.EventRecorder.Event(&aas, corev1.EventTypeNormal, reason, message)
}

// formatClassifiedMessage builds the structured event message used both
// for Grafana annotations and operator-visible kubectl describe output.
func formatClassifiedMessage(r PipelineResult) string {
	return fmt.Sprintf(
		"pattern=%s confidence=%s historyPoints=%d "+
			"scaleUpCooldown=%ds scaleDownCooldown=%ds maxStep=%d forecaster=%s",
		r.Pattern, r.Confidence, r.HistoryPoints,
		r.Params.ScaleUpCooldown, r.Params.ScaleDownCooldown,
		r.Params.MaxStep, r.Params.PreferredForecaster)
}

// historyPointsAsInt32 narrows an int to int32 with explicit saturation.
// The HistoryPoints series can never realistically exceed 2³¹ samples in
// our deployments, but the conversion is still proven safe for gosec.
func historyPointsAsInt32(v int) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v)
}
