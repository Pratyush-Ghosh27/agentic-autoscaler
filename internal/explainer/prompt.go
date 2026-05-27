/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package explainer implements the ExplainWorker, which generates plain
// English scaling explanations using Ollama. See docs/design.md §6.2.
package explainer

import (
	"fmt"
	"strings"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/controller"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/reasoning"
)

// SystemMessage is the fixed persona / instruction sent to Ollama on
// every Explain call. Stable across all requests so the model's output
// doesn't drift between scale events. Keep it short — long system
// prompts inflate latency and inflate the rate-limit risk on shared
// LLM gateways.
const SystemMessage = "You are observing a Kubernetes autoscaler. " +
	"Explain scaling decisions in 2-3 plain English sentences. " +
	"Be concise, specific, and ground your explanation in the data provided."

// MaxEventLength is the K8s Event message length budget. Anything past
// this is truncated by TrimContent before emission.
const MaxEventLength = 500

// BuildPrompt constructs the (system, user) messages for an Ollama call.
// Pure function — no I/O — so it can be unit-tested with table-driven cases.
//
// The user message intentionally enumerates the data points instead of
// asking the LLM to interpret raw JSON: small models hallucinate less when
// the schema is hand-rendered into prose.
//
// Conditional rendering rules from design_v2.md §6.2:
//   - Omit the "Traffic pattern" line entirely when Pattern is "" or "default"
//     (those mean "classifier hasn't run yet" or "no recognised pattern").
//   - F12: Include the "Long-term context" line whenever Pattern != ""
//     (NOT gated on Pattern != "default" — a workload classified as
//     "default" still has measured baseline/p95 worth surfacing).
//   - Only include the "limited by maxStep" line when the step cap actually
//     activated (Reason ∈ {step_capped_up, step_capped_down}).
func BuildPrompt(req controller.ExplainRequest) (system, user string) {
	var b strings.Builder

	if req.Pattern != "" && req.Pattern != "default" {
		fmt.Fprintf(&b, "Traffic pattern: %s (confidence: %s)\n",
			req.Pattern, req.Confidence)
	}

	// F12: Long-term context gates only on Pattern != "" (NOT != "default") —
	// a workload classified as "default" still has measured baseline/p95.
	if req.Pattern != "" {
		fmt.Fprintf(&b, "Long-term context: baseline %d rps, p95 %d rps\n",
			req.BaselineRPS, req.PeakP95RPS)
	}

	fmt.Fprintf(&b, "Current RPS: %.1f, Predicted RPS (%d min ahead): %.1f\n",
		req.CurrentRPS, req.HorizonMinutes, req.PredictedRPS)
	fmt.Fprintf(&b, "Scaling: %d → %d replicas (%s)\n",
		req.CurrentReplicas, req.TargetReplicas, req.Reason)

	if req.Reason == reasoning.StepCappedUp || req.Reason == reasoning.StepCappedDown {
		fmt.Fprintf(&b,
			"This scale was limited by maxStep: the controller computed %d replicas from the forecast but moved only to %d this reconcile (cap: %d replicas per reconcile).\n",
			req.RecommendedReplicas, req.TargetReplicas, req.EffectiveMaxStep)
	}

	// F33: max_replicas_binding conditional. Without this prose, the LLM sees
	// only "Scaling: 5 → 10 (max_replicas_binding)" and generates misleading
	// "scaled up to handle load" text — hiding the capacity-planning signal.
	if req.Reason == reasoning.MaxReplicasBinding {
		fmt.Fprintf(&b,
			"This scale was limited by maxReplicas: the forecast asked for %d replicas but the CRD bound capped it at maxReplicas=%d. Raise spec.maxReplicas to let the autoscaler scale further.\n",
			req.UnboundedRecommended, req.MaxReplicas)
	}

	if req.Reason == reasoning.MinReplicasBinding {
		fmt.Fprintf(&b,
			"This scale was limited by minReplicas: the forecast asked for only %d replicas but the CRD bound floored it at minReplicas=%d.\n",
			req.UnboundedRecommended, req.MinReplicas)
	}

	fmt.Fprintf(&b, "Forecasting model: %s\n", req.ModelUsed)
	fmt.Fprintf(&b,
		"Active parameters: scaleUpCooldown=%ds, scaleDownCooldown=%ds, maxStep=%d\n",
		req.EffectiveCooldownUp, req.EffectiveCooldownDown, req.EffectiveMaxStep)
	fmt.Fprintf(&b, "\nExplain why this decision was made and what the traffic data suggests "+
		"relative to the workload's long-term baseline.")

	return SystemMessage, b.String()
}

// TrimContent enforces MaxEventLength on the LLM output before it lands in
// a K8s Event. K8s itself enforces a 1024-byte limit on Event messages but
// 500 keeps the kubectl describe output readable on a single screen.
//
// When truncation occurs, we append "..." (3 chars) so operators know the
// message was cut. The returned string is always exactly maxLen chars on
// truncation — no off-by-one ambiguity.
func TrimContent(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
