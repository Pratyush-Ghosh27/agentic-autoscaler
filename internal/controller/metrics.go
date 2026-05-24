/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/classifier"
)

// patternEnum maps classifier.Pattern* string constants to the float64
// values exposed by the agenticautoscaler_classified_pattern gauge.
// Values are stable because Grafana uses them as panel "value mappings"
// — changing a number breaks the dashboard. New patterns must extend
// this map and the dashboard's value mappings together. The 0 slot is
// reserved for "no classification yet" so a freshly-deployed CR doesn't
// look like flat traffic.
var patternEnum = map[string]float64{
	"":                            0,
	classifier.PatternDefault:     0,
	classifier.PatternFlat:        1,
	classifier.PatternPeriodic:    2,
	classifier.PatternSpiky:       3,
	classifier.PatternGradualRamp: 4,
}

// confidenceEnum maps Pipeline confidence strings to a numeric value
// for the same dashboard reason as patternEnum.
var confidenceEnum = map[string]float64{
	"low":    0,
	"medium": 1,
	"high":   2,
}

// All metrics use a `agenticautoscaler_` prefix to keep them clearly
// scoped to this controller in a shared Prometheus tenant. Labels are
// always (namespace, name) — the natural primary key for an AAS CR —
// and are kept low-cardinality.
var (
	// mPredictedRPS is the most recent predicted_rps the forecast service
	// returned, in requests/second. Replaces the dashboard's dead query.
	mPredictedRPS = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "agenticautoscaler_predicted_rps",
			Help: "Most recent forecast service predicted RPS, per AAS CR.",
		},
		[]string{"namespace", "name"},
	)

	// mCurrentRPS mirrors what the controller's instant query returned
	// on the last reconcile. Useful for a "live observed RPS" dashboard
	// row that doesn't depend on target-app emitting custom metrics.
	mCurrentRPS = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "agenticautoscaler_current_rps",
			Help: "Most recent observed RPS used as forecast input, per AAS CR.",
		},
		[]string{"namespace", "name"},
	)

	// mRpsPerPod is the controller's current sliding-window
	// median of rps/pod, post-clamp. Surfaces the otherwise-invisible
	// state used by ComputeRecommended.
	mRpsPerPod = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "agenticautoscaler_rps_per_pod",
			Help: "Current sliding-window median rps/pod (clamped), per AAS CR.",
		},
		[]string{"namespace", "name"},
	)

	// mScaleEventsTotal increments every time the controller decides on
	// a target replica count, labelled by the reasoning token from
	// internal/reasoning so operators can answer "why didn't we scale?".
	mScaleEventsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agenticautoscaler_events_total",
			Help: "Cumulative count of scale-decision events, by reason token.",
		},
		[]string{"namespace", "name", "reason"},
	)

	// mClassifiedPattern is the most recent classified pattern,
	// reported as an integer enum (see patternEnum). The dashboard
	// uses Grafana value-mappings to render the human-readable name.
	mClassifiedPattern = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "agenticautoscaler_classified_pattern",
			Help: "Most recent classifier output, encoded as an integer enum (0=unknown,1=flat,2=bursty,3=diurnal,4=spiky_tod,5=stochastic).",
		},
		[]string{"namespace", "name"},
	)

	// mClassifiedConfidence is the most recent classifier confidence,
	// encoded as 0=low / 1=medium / 2=high.
	mClassifiedConfidence = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "agenticautoscaler_classified_confidence",
			Help: "Classifier confidence as integer enum (0=low,1=medium,2=high).",
		},
		[]string{"namespace", "name"},
	)

	// mForecastFailuresTotal increments on every forecast service error
	// (transport, decode, or non-2xx). Distinct from
	// `controller_runtime_reconcile_errors_total` because forecast
	// failures don't fail the reconcile — they just degrade scaling
	// quality, and operators want to alert on a sustained baseline.
	mForecastFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agenticautoscaler_forecast_failures_total",
			Help: "Cumulative count of forecast service failures (does not increment on success).",
		},
		[]string{"namespace", "name"},
	)

	// mMetricsUnavailableTotal increments on every Prometheus query
	// failure (instant or range, including insufficient-points).
	mMetricsUnavailableTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agenticautoscaler_metrics_unavailable_total",
			Help: "Cumulative count of reconciles that aborted because Prometheus data was missing or insufficient.",
		},
		[]string{"namespace", "name"},
	)
)

// init wires every metric into the controller-runtime metrics registry,
// which is what the Manager's metrics endpoint serves at :8080/metrics.
// MustRegister panics on a duplicate register, which is correct here:
// double-registration means a packaging bug that should fail at startup.
func init() {
	metrics.Registry.MustRegister(
		mPredictedRPS,
		mCurrentRPS,
		mRpsPerPod,
		mScaleEventsTotal,
		mClassifiedPattern,
		mClassifiedConfidence,
		mForecastFailuresTotal,
		mMetricsUnavailableTotal,
	)
}

// observeReconcile records all the per-reconcile gauges and counters
// in one place. Called from the reconciler's hot path right after each
// scaling decision is made.
//
// reason is the reasoning token that controlled the decision (e.g.
// reasoning.ScaledUp, reasoning.CooldownGated). currentRpsVal,
// predictedRpsVal and rpsPerPodVal are recorded as gauges; scaleReason
// increments the events counter.
func observeReconcile(namespace, name, reason string, currentRpsVal, predictedRpsVal, rpsPerPodVal float64) {
	mCurrentRPS.WithLabelValues(namespace, name).Set(currentRpsVal)
	mPredictedRPS.WithLabelValues(namespace, name).Set(predictedRpsVal)
	mRpsPerPod.WithLabelValues(namespace, name).Set(rpsPerPodVal)
	mScaleEventsTotal.WithLabelValues(namespace, name, reason).Inc()
}

// observeClassification records the per-classification gauges. Called
// from the classifier worker via the Manager's hook (this commit doesn't
// add the hook — that's a follow-up; for now the reconciler picks the
// pattern out of the CR's status if the classifier has populated it).
func observeClassification(namespace, name, pattern, confidence string) {
	if v, ok := patternEnum[pattern]; ok {
		mClassifiedPattern.WithLabelValues(namespace, name).Set(v)
	}
	if v, ok := confidenceEnum[confidence]; ok {
		mClassifiedConfidence.WithLabelValues(namespace, name).Set(v)
	}
}

// observeForecastFailure increments forecast_failures_total. Called on
// every forecast service error path in the reconciler.
func observeForecastFailure(namespace, name string) {
	mForecastFailuresTotal.WithLabelValues(namespace, name).Inc()
}

// observeMetricsUnavailable increments metrics_unavailable_total. Called
// on every Prometheus instant or range query failure, and on the
// insufficient-history short-circuit.
func observeMetricsUnavailable(namespace, name string) {
	mMetricsUnavailableTotal.WithLabelValues(namespace, name).Inc()
}
