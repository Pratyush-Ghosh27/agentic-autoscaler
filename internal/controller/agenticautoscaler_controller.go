/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"
	"fmt"
	"math"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	autoscalingv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/api/v1alpha1"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/forecast"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/classifier"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/config"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/decision"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/promql"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/reasoning"
)

// ringBufferCapacity is the number of rps_per_pod observations retained for
// the sliding-window median. 10 reconciles at the default 60s interval is
// 10 minutes of memory — long enough to smooth bursts, short enough to track
// genuine workload changes.
const ringBufferCapacity = 10

// AgenticAutoscalerReconciler reconciles an AgenticAutoscaler object.
//
// The reconcile loop implements design.md §5 step-by-step. Every subsystem
// the loop talks to (Prometheus, Forecast Service, ExplainWorker) is
// injected behind an interface so the orchestration logic can be exercised
// with in-memory fakes inside envtest.
type AgenticAutoscalerReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	EventRecorder record.EventRecorder
	Config        *config.Config
	PromQuerier   PromQuerier
	Forecaster    Forecaster
	ExplainNotify ExplainNotifier
	StateStore    *decision.StateStore
	// Classifier owns the ClassifierWorker goroutine for each CR. May be
	// nil in unit tests that exercise pure reconcile behaviour without
	// the cold-path; production wiring (cmd/controller/main.go) always
	// supplies a real manager.
	Classifier *classifier.Manager
	// Now is injected for testability. Defaults to time.Now via nowFunc().
	Now func() time.Time
}

// +kubebuilder:rbac:groups=autoscaling.agentic.io,resources=agenticautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling.agentic.io,resources=agenticautoscalers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=autoscaling.agentic.io,resources=agenticautoscalers/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments/scale,verbs=get;update;patch
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is the main entry point. It implements design.md §5 step-by-step:
//  1. Pre-checks (kill switch, deletion, HPA conflict).
//  2. Prometheus instant + range queries.
//  3. POST /recommend to the Forecast Service.
//     4-5. Update sliding-window rps_per_pod estimate.
//  5. ComputeRecommended (pre-cap, pre-cooldown).
//     6-8. ApplyCapAndCooldown (step cap, cooldown gate, hysteresis).
//  9. Patch /scale subresource if a change is needed.
//  10. Emit a K8s Event with the reasoning token + notify ExplainWorker.
//  11. Persist updated status.
//
// Every failure mode (Prometheus down, forecast down, /scale fail) returns
// nil error and a requeue, so transient outages do not interfere with
// controller-runtime's exponential backoff for genuine programming errors.
func (r *AgenticAutoscalerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	var aas autoscalingv1alpha1.AgenticAutoscaler
	if err := r.Get(ctx, req.NamespacedName, &aas); err != nil {
		if client.IgnoreNotFound(err) == nil {
			r.StateStore.Delete(req.NamespacedName)
			if r.Classifier != nil {
				r.Classifier.Stop(req.NamespacedName)
			}
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Cold-path lifecycle: ensure a ClassifierWorker is running for this
	// CR (idempotent; no-op once the worker is started). The worker's
	// own immediate-first-run trigger handles initial classification.
	if r.Classifier != nil {
		r.Classifier.Ensure(&aas)
		if aas.Annotations[reasoning.AnnotationReclassify] == "true" {
			r.Classifier.SignalReclassify(req.NamespacedName)
		}
	}

	// Pre-check 1a: kill-switch.
	if aas.Annotations[reasoning.AnnotationKillSwitch] == "true" {
		return r.handleKillSwitch(ctx, &aas)
	}

	// Pre-check 1b: deletion → IgnoreNotFound above + finalizer-free design.
	if !aas.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Pre-check 1c: HPA conflict.
	if conflicting, hpaName, err := r.findConflictingHPA(ctx, &aas); err != nil {
		return ctrl.Result{}, err
	} else if conflicting {
		return r.handleConflict(ctx, &aas, hpaName)
	}

	// Step 2: Prometheus queries.
	deployName := aas.Spec.TargetRef.Name
	currentRPS, err := r.PromQuerier.InstantQuery(ctx, promql.InstantRPS(deployName))
	if err != nil {
		log.Error(err, "prometheus instant query failed")
		r.EventRecorder.Event(&aas, corev1.EventTypeWarning, reasoning.MetricsUnavailable, err.Error())
		observeMetricsUnavailable(aas.Namespace, aas.Name)
		return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
	}

	historyEnd := r.now()
	historyStart := historyEnd.Add(-r.Config.HotPathHistory)
	samples, err := r.PromQuerier.RangeQuery(ctx, promql.RangeRPS(deployName), historyStart, historyEnd, time.Minute)
	if err != nil {
		log.Error(err, "prometheus range query failed")
		r.EventRecorder.Event(&aas, corev1.EventTypeWarning, reasoning.MetricsUnavailable, err.Error())
		observeMetricsUnavailable(aas.Namespace, aas.Name)
		return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
	}
	if len(samples) < int(r.Config.HotPathMinPoints) {
		msg := fmt.Sprintf("only %d range samples (need %d)", len(samples), r.Config.HotPathMinPoints)
		r.EventRecorder.Event(&aas, corev1.EventTypeWarning, reasoning.MetricsUnavailable, msg)
		observeMetricsUnavailable(aas.Namespace, aas.Name)
		return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
	}
	rpsHistory := make([]float64, len(samples))
	for i, s := range samples {
		rpsHistory[i] = s.Value
	}

	// Step 3+4: forecast call.
	effective := decision.ResolveEffectiveParams(r.buildParamSources(&aas))
	preferredModel := effective.Forecaster
	if preferredModel == "auto" {
		preferredModel = ""
	}
	forecastResp, err := r.Forecaster.Recommend(ctx, forecast.RecommendRequest{
		RpsHistory:     rpsHistory,
		WorkloadID:     req.NamespacedName.String(),
		PreferredModel: preferredModel,
		Context:        r.buildForecastContext(&aas),
	})
	if err != nil {
		log.Error(err, "forecast service call failed")
		r.EventRecorder.Event(&aas, corev1.EventTypeWarning, reasoning.ForecastUnavailable, err.Error())
		observeForecastFailure(aas.Namespace, aas.Name)
		return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
	}

	// Step 5: rps_per_pod state.
	var deploy appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: aas.Namespace, Name: deployName}, &deploy); err != nil {
		log.Error(err, "failed to get target Deployment")
		return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
	}
	// Generation-change detection (design §6.1 trigger 4). The classifier
	// manager dedups against the last seen value, so this call is
	// safe to make on every reconcile. Skipped when Classifier is nil.
	if r.Classifier != nil {
		r.Classifier.ObserveDeploymentGeneration(req.NamespacedName, deploy.Generation)
	}
	currentReplicas := int32(1)
	if deploy.Spec.Replicas != nil {
		currentReplicas = *deploy.Spec.Replicas
	}

	state := r.StateStore.GetOrCreate(req.NamespacedName, ringBufferCapacity)
	if !state.Initialized {
		decision.InitializeFromStatus(state, r.buildStatusSeed(&aas))
	}

	rpsPerPodMin := derefOr(aas.Spec.RpsPerPodMin, 50)
	rpsPerPodMax := derefOr(aas.Spec.RpsPerPodMax, 500)
	now := r.now()
	lastScale := laterOf(state.LastScaleUpTime, state.LastScaleDownTime)
	if decision.ShouldUpdateRpsPerPodWithFloor(
		currentRPS, currentReplicas, lastScale, now,
		r.Config.ReconcileInterval, float64(r.Config.RpsPerPodNoiseFloorRPS),
	) {
		state.Observations.Push(currentRPS / float64(currentReplicas))
		state.RpsPerPod = state.Observations.Median()
	}
	rpsPerPod := decision.ClampRpsPerPod(state.RpsPerPod, rpsPerPodMin, rpsPerPodMax)

	// Step 5 (cont.): pre-cap recommendation.
	minReplicas := derefOr(aas.Spec.MinReplicas, 2)
	maxReplicas := derefOr(aas.Spec.MaxReplicas, 10)
	recommended := decision.ComputeRecommended(forecastResp.PredictedRPS, rpsPerPod, minReplicas, maxReplicas)

	// Step 6-8: cap + cooldown + hysteresis.
	capOut := decision.ApplyCapAndCooldown(decision.CapInput{
		Recommended:   recommended,
		Current:       currentReplicas,
		MaxStep:       effective.MaxStep,
		CooldownUp:    effective.CooldownUp,
		CooldownDown:  effective.CooldownDown,
		LastScaleUp:   state.LastScaleUpTime,
		LastScaleDown: state.LastScaleDownTime,
		Now:           now,
	})

	// Step 9: patch /scale.
	if capOut.ShouldPatch {
		scale := &autoscalingv1.Scale{
			ObjectMeta: metav1.ObjectMeta{
				Name:      deploy.Name,
				Namespace: deploy.Namespace,
			},
			Spec: autoscalingv1.ScaleSpec{Replicas: capOut.Target},
		}
		if err := r.SubResource("scale").Update(ctx, &deploy, client.WithSubResourceBody(scale)); err != nil {
			log.Error(err, "failed to patch /scale subresource")
			return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
		}
		if capOut.Target > currentReplicas {
			state.LastScaleUpTime = now
		} else {
			state.LastScaleDownTime = now
		}
	}

	// Step 10: emit Event + notify ExplainWorker.
	r.EventRecorder.Eventf(&aas, corev1.EventTypeNormal, capOut.Reason,
		"current_rps=%.1f predicted_rps=%.1f current=%d target=%d model=%s",
		currentRPS, forecastResp.PredictedRPS, currentReplicas, capOut.Target, forecastResp.ModelUsed)

	// Record per-reconcile gauges + scale-events counter. Done after
	// the decision, before status update, so a status-update failure
	// doesn't double-count.
	observeReconcile(aas.Namespace, aas.Name, capOut.Reason,
		currentRPS, forecastResp.PredictedRPS, rpsPerPod)
	observeClassification(aas.Namespace, aas.Name,
		classifiedPattern(&aas), classifiedConfidence(&aas))

	if capOut.ShouldPatch {
		r.ExplainNotify.Notify(ExplainRequest{
			Namespace:             aas.Namespace,
			Name:                  aas.Name,
			Reason:                capOut.Reason,
			CurrentReplicas:       currentReplicas,
			RecommendedReplicas:   recommended,
			TargetReplicas:        capOut.Target,
			CurrentRPS:            currentRPS,
			PredictedRPS:          forecastResp.PredictedRPS,
			HorizonMinutes:        forecastResp.HorizonMinutes,
			ModelUsed:             forecastResp.ModelUsed,
			Pattern:               classifiedPattern(&aas),
			Confidence:            classifiedConfidence(&aas),
			EffectiveCooldownUp:   effective.CooldownUp,
			EffectiveCooldownDown: effective.CooldownDown,
			EffectiveMaxStep:      effective.MaxStep,
		})
	}

	// Step 11: status update.
	aas.Status.Phase = autoscalingv1alpha1.PhaseReady
	aas.Status.ConflictReason = ""
	aas.Status.CurrentReplicas = capOut.Target
	aas.Status.RecommendedReplicas = recommended
	aas.Status.PredictedRPS = int32(forecastResp.PredictedRPS)
	aas.Status.RpsPerPodCurrent = int32(rpsPerPod)
	if capOut.ShouldPatch {
		t := metav1.NewTime(now)
		aas.Status.LastScaleTime = &t
	}
	if err := r.Status().Update(ctx, &aas); err != nil {
		log.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
}

// handleKillSwitch transitions the CR to phase=Disabled and emits a single
// Event. We deliberately do NOT call the forecast service or patch /scale
// — the kill switch is the operator's "stop touching this Deployment"
// pull-cord.
func (r *AgenticAutoscalerReconciler) handleKillSwitch(ctx context.Context, aas *autoscalingv1alpha1.AgenticAutoscaler) (ctrl.Result, error) {
	if aas.Status.Phase != autoscalingv1alpha1.PhaseDisabled {
		aas.Status.Phase = autoscalingv1alpha1.PhaseDisabled
		if err := r.Status().Update(ctx, aas); err != nil {
			return ctrl.Result{}, err
		}
		r.EventRecorder.Event(aas, corev1.EventTypeWarning, reasoning.KillSwitched,
			"kill-switch annotation present; controller paused")
	}
	return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
}

// findConflictingHPA returns (true, hpaName, nil) if any HPA in the same
// namespace targets the same Deployment as this CR. The check is namespaced
// because HPA can only target Deployments in its own namespace.
func (r *AgenticAutoscalerReconciler) findConflictingHPA(ctx context.Context, aas *autoscalingv1alpha1.AgenticAutoscaler) (bool, string, error) {
	var hpaList autoscalingv2.HorizontalPodAutoscalerList
	if err := r.List(ctx, &hpaList, client.InNamespace(aas.Namespace)); err != nil {
		return false, "", err
	}
	for _, hpa := range hpaList.Items {
		ref := hpa.Spec.ScaleTargetRef
		if ref.Kind == aas.Spec.TargetRef.Kind && ref.Name == aas.Spec.TargetRef.Name {
			return true, hpa.Name, nil
		}
	}
	return false, "", nil
}

// handleConflict transitions the CR to phase=Conflict, emits an Event, and
// requeues. The reconciler will retry on its normal cadence; if the operator
// removes the offending HPA, the next reconcile clears the phase back to
// Ready (via the inline status reset in the happy-path body).
func (r *AgenticAutoscalerReconciler) handleConflict(ctx context.Context, aas *autoscalingv1alpha1.AgenticAutoscaler, hpaName string) (ctrl.Result, error) {
	reason := fmt.Sprintf("HPA %q already manages this Deployment", hpaName)
	if aas.Status.Phase != autoscalingv1alpha1.PhaseConflict || aas.Status.ConflictReason != reason {
		aas.Status.Phase = autoscalingv1alpha1.PhaseConflict
		aas.Status.ConflictReason = reason
		if err := r.Status().Update(ctx, aas); err != nil {
			return ctrl.Result{}, err
		}
		r.EventRecorder.Event(aas, corev1.EventTypeWarning, reasoning.ConflictDetected, reason)
	}
	return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
}

// buildParamSources translates the CR's Spec + Status into the input shape
// expected by decision.ResolveEffectiveParams.
func (r *AgenticAutoscalerReconciler) buildParamSources(aas *autoscalingv1alpha1.AgenticAutoscaler) decision.ParamSources {
	src := decision.ParamSources{
		Spec: decision.SpecParams{
			ScaleUpCooldown:     aas.Spec.ScaleUpCooldownSeconds,
			ScaleDownCooldown:   aas.Spec.ScaleDownCooldownSeconds,
			MaxStepSize:         aas.Spec.MaxStepSize,
			PreferredForecaster: aas.Spec.PreferredForecaster,
		},
		Defaults: decision.DefaultParams{
			ScaleUpCooldown:   durationSecondsAsInt32(r.Config.DefaultScaleUpCooldown),
			ScaleDownCooldown: durationSecondsAsInt32(r.Config.DefaultScaleDownCooldown),
			MaxStepSize:       r.Config.DefaultMaxStepSize,
		},
	}
	if cp := aas.Status.ClassifiedParams; cp != nil {
		src.Classified = &decision.ClassifiedParams{
			ScaleUpCooldown:     cp.ScaleUpCooldownSeconds,
			ScaleDownCooldown:   cp.ScaleDownCooldownSeconds,
			MaxStepSize:         cp.MaxStepSize,
			PreferredForecaster: cp.PreferredForecaster,
		}
	}
	return src
}

// AnnotationSkipContext, when set to "true", suppresses forwarding of
// the cold-path-computed Context block on /recommend even if status
// has it. Used for canary / fallback debugging when an operator wants
// to compare forecasts with vs. without context. G10.
const AnnotationSkipContext = "autoscaling.agentic.io/skip-context"

// buildForecastContext converts status.classifiedParams.context into
// a wire-side ContextPayload, stamping CurrentHourUTC and
// CurrentMinuteUTC from the controller's clock. Returns nil when:
//   - status.classifiedParams is missing (cold start, before the
//     classifier worker has produced anything), or
//   - status.classifiedParams.context is missing (legacy data), or
//   - the operator has set the skip-context annotation.
//
// nil is the "omitempty" signal — the JSON encoder drops the field
// from the wire so the Forecast Service treats the request as
// context-less. G10.
func (r *AgenticAutoscalerReconciler) buildForecastContext(aas *autoscalingv1alpha1.AgenticAutoscaler) *forecast.ContextPayload {
	if aas.Annotations[AnnotationSkipContext] == "true" {
		return nil
	}
	cp := aas.Status.ClassifiedParams
	if cp == nil || cp.Context == nil {
		return nil
	}
	now := r.now().UTC()
	return &forecast.ContextPayload{
		BaselineRPS:        cp.Context.BaselineRPS,
		PeakP95RPS:         cp.Context.PeakP95RPS,
		Trend24hSlope:      cp.Context.Trend24hSlope,
		HourlyProfile:      cp.Context.HourlyProfile,
		HourlyProfileValid: cp.Context.HourlyProfileValid,
		CurrentHourUTC:     int32(now.Hour()),
		CurrentMinuteUTC:   int32(now.Minute()),
	}
}

// buildStatusSeed converts the persisted Status into a StatusSeed used for
// restart recovery. RpsPerPodCurrent is treated as in-bounds when it is
// inside [rpsPerPodMin, rpsPerPodMax]; otherwise the seed falls through to
// the midpoint.
func (r *AgenticAutoscalerReconciler) buildStatusSeed(aas *autoscalingv1alpha1.AgenticAutoscaler) decision.StatusSeed {
	min := derefOr(aas.Spec.RpsPerPodMin, 50)
	max := derefOr(aas.Spec.RpsPerPodMax, 500)
	current := float64(aas.Status.RpsPerPodCurrent)
	inBounds := current >= float64(min) && current <= float64(max)
	seed := decision.StatusSeed{
		RpsPerPodCurrent: current,
		InBounds:         inBounds,
		Midpoint:         float64(min+max) / 2.0,
	}
	if aas.Status.LastScaleTime != nil {
		seed.LastScaleTime = aas.Status.LastScaleTime.Time
	}
	return seed
}

func (r *AgenticAutoscalerReconciler) requeueInterval() time.Duration {
	return r.Config.ReconcileInterval
}

func (r *AgenticAutoscalerReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// SetupWithManager registers the reconciler with the controller-runtime
// manager. Watching only AgenticAutoscaler — Deployment and HPA changes that
// affect us are picked up on the next periodic requeue.
func (r *AgenticAutoscalerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&autoscalingv1alpha1.AgenticAutoscaler{}).
		Named("agenticautoscaler").
		Complete(r)
}

// derefOr returns *p when p != nil, otherwise def.
func derefOr(p *int32, def int32) int32 {
	if p == nil {
		return def
	}
	return *p
}

// laterOf returns whichever of a, b is later.
func laterOf(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// classifiedPattern returns the pattern name from status.classifiedParams,
// or the empty string when the classifier has not yet run for this CR.
// The empty string is the explicit "no classification yet" signal.
func classifiedPattern(aas *autoscalingv1alpha1.AgenticAutoscaler) string {
	if aas.Status.ClassifiedParams == nil {
		return ""
	}
	return aas.Status.ClassifiedParams.Pattern
}

// classifiedConfidence mirrors classifiedPattern for the confidence field.
func classifiedConfidence(aas *autoscalingv1alpha1.AgenticAutoscaler) string {
	if aas.Status.ClassifiedParams == nil {
		return ""
	}
	return aas.Status.ClassifiedParams.Confidence
}

// durationSecondsAsInt32 expresses a Duration in seconds, clamped to the
// signed-32-bit range. Cooldown defaults are well inside this range
// (single-digit minutes); the clamp exists purely so the conversion is
// proven safe to gosec rather than a benign-but-flagged narrowing.
func durationSecondsAsInt32(d time.Duration) int32 {
	v := int64(d / time.Second)
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v)
}
