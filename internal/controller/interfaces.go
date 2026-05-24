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
	"time"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/forecast"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/prometheus"
)

// PromQuerier is the surface area of *prometheus.Client (Plan #3) that the
// reconciler depends on. The interface lives here, not in the prometheus
// package, so envtest can substitute fakes without an extra import edge.
type PromQuerier interface {
	InstantQuery(ctx context.Context, query string) (float64, error)
	RangeQuery(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]prometheus.Sample, error)
}

// Forecaster is the surface area of *forecast.Client (Plan #3).
type Forecaster interface {
	Recommend(ctx context.Context, req forecast.RecommendRequest) (forecast.RecommendResponse, error)
}

// ExplainRequest carries the context for a scale-explanation LLM call. It is
// produced by the reconciler on every replica-changing event and consumed by
// the ExplainWorker (Plan #6). Field set matches docs/design.md §6.2 — adding
// or renaming a field requires updating the prompt template in
// internal/explainer/prompt.go.
type ExplainRequest struct {
	Namespace             string
	Name                  string
	Reason                string  // reasoning token from internal/reasoning
	CurrentReplicas       int32
	RecommendedReplicas   int32   // pre-cap, pre-cooldown recommendation
	TargetReplicas        int32   // post-cap (what was patched onto /scale)
	CurrentRPS            float64
	PredictedRPS          float64
	HorizonMinutes        int     // forecast horizon
	ModelUsed             string  // forecast model name
	Pattern               string  // status.classifiedParams.pattern; "" if not yet classified
	Confidence            string  // "high"/"medium"/"" (mirrors Pattern's nil semantics)
	EffectiveCooldownUp   int32
	EffectiveCooldownDown int32
	EffectiveMaxStep      int32
}

// ExplainNotifier signals the ExplainWorker of a scaling event. The contract
// is "fire and forget, never block": if the worker is busy, the event is
// dropped or replaced — never queued. See ChannelNotifier for the concrete
// drop-and-replace implementation.
type ExplainNotifier interface {
	Notify(req ExplainRequest)
}

// ChannelNotifier implements ExplainNotifier using a buffered channel of
// capacity one with drop-and-replace semantics. On Notify we:
//  1. Non-blockingly drain any already-queued event (it's stale).
//  2. Non-blockingly enqueue the new event.
//
// Both selects use a default branch so the reconciler never blocks even if
// the channel is somehow saturated. See docs/design.md §6.2.
type ChannelNotifier struct {
	Ch chan ExplainRequest
}

// Notify implements ExplainNotifier with drop-and-replace semantics.
func (cn ChannelNotifier) Notify(req ExplainRequest) {
	select {
	case <-cn.Ch:
	default:
	}
	select {
	case cn.Ch <- req:
	default:
	}
}
