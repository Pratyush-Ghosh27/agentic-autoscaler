/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package decision implements the pure scaling decision logic for the
// AgenticAutoscaler controller. It has zero I/O, zero Kubernetes imports
// beyond the simple value types, and zero calls to time.Now (the clock
// is injected via the Now field on each input struct). This makes every
// quirk of design.md §5 testable from a Go unit test in microseconds.
package decision

import (
	"math"
	"time"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/reasoning"
)

// ParamSources feeds the nil-coalesce resolution chain (design §5 preamble:
// "spec ?? classified ?? defaults" for each tunable).
type ParamSources struct {
	Spec       SpecParams
	Classified *ClassifiedParams // nil when cold-start (no classifier run yet)
	Defaults   DefaultParams
}

// SpecParams mirrors the optional pointer fields from AgenticAutoscalerSpec.
type SpecParams struct {
	ScaleUpCooldown     *int32
	ScaleDownCooldown   *int32
	MaxStepSize         *int32
	PreferredForecaster *string
}

// ClassifiedParams mirrors the (always-populated) classifier output.
type ClassifiedParams struct {
	ScaleUpCooldown     int32
	ScaleDownCooldown   int32
	MaxStepSize         int32
	PreferredForecaster string
}

// DefaultParams holds the env-var-driven hard defaults.
type DefaultParams struct {
	ScaleUpCooldown   int32
	ScaleDownCooldown int32
	MaxStepSize       int32
}

// EffectiveParams is the resolved output of the nil-coalesce chain.
type EffectiveParams struct {
	CooldownUp   int32
	CooldownDown int32
	MaxStep      int32
	Forecaster   string // "prophet", "linear_extrap", or "auto"
}

// ResolveEffectiveParams applies spec ?? classified ?? defaults for each field.
// Empty/zero string for PreferredForecaster is treated as "absent" so callers
// can leave a Spec.PreferredForecaster pointer set to a zero string without
// shadowing classified output.
func ResolveEffectiveParams(src ParamSources) EffectiveParams {
	return EffectiveParams{
		CooldownUp: coalesce32(
			src.Spec.ScaleUpCooldown,
			classifiedField32(src.Classified, func(c *ClassifiedParams) int32 { return c.ScaleUpCooldown }),
			src.Defaults.ScaleUpCooldown,
		),
		CooldownDown: coalesce32(
			src.Spec.ScaleDownCooldown,
			classifiedField32(src.Classified, func(c *ClassifiedParams) int32 { return c.ScaleDownCooldown }),
			src.Defaults.ScaleDownCooldown,
		),
		MaxStep: coalesce32(
			src.Spec.MaxStepSize,
			classifiedField32(src.Classified, func(c *ClassifiedParams) int32 { return c.MaxStepSize }),
			src.Defaults.MaxStepSize,
		),
		Forecaster: coalesceStr(
			src.Spec.PreferredForecaster,
			classifiedFieldStr(src.Classified, func(c *ClassifiedParams) string { return c.PreferredForecaster }),
			"auto",
		),
	}
}

func coalesce32(spec *int32, classified *int32, def int32) int32 {
	if spec != nil {
		return *spec
	}
	if classified != nil {
		return *classified
	}
	return def
}

func classifiedField32(c *ClassifiedParams, f func(*ClassifiedParams) int32) *int32 {
	if c == nil {
		return nil
	}
	v := f(c)
	return &v
}

func coalesceStr(spec *string, classified *string, def string) string {
	if spec != nil && *spec != "" {
		return *spec
	}
	if classified != nil && *classified != "" {
		return *classified
	}
	return def
}

func classifiedFieldStr(c *ClassifiedParams, f func(*ClassifiedParams) string) *string {
	if c == nil {
		return nil
	}
	v := f(c)
	if v == "" {
		return nil
	}
	return &v
}

// ComputeRecommended calculates the raw recommendedReplicas (pre-cap,
// pre-cooldown), per design §5 step 5: ceil(predicted / rps_per_pod) clamped
// to [minReplicas, maxReplicas]. If rps_per_pod <= 0 we fail safe to
// maxReplicas — the math is undefined and we'd rather over-provision than
// under-provision in that edge case.
func ComputeRecommended(predictedRPS, rpsPerPod float64, minReplicas, maxReplicas int32) int32 {
	if rpsPerPod <= 0 {
		return maxReplicas
	}
	raw := int32(math.Ceil(predictedRPS / rpsPerPod))
	if raw < minReplicas {
		return minReplicas
	}
	if raw > maxReplicas {
		return maxReplicas
	}
	return raw
}

// CapInput feeds ApplyCapAndCooldown.
type CapInput struct {
	Recommended   int32
	Current       int32
	MaxStep       int32
	CooldownUp    int32 // seconds
	CooldownDown  int32 // seconds
	LastScaleUp   time.Time
	LastScaleDown time.Time
	Now           time.Time
}

// CapOutput is the result of applying step cap + cooldown + hysteresis.
type CapOutput struct {
	// Target is the replica count to patch. Equals Current when ShouldPatch
	// is false (the cooldown / hysteresis cases).
	Target int32
	// Reason is a stable reasoning token from internal/reasoning/tokens.go.
	Reason string
	// ShouldPatch signals whether the controller should call the /scale
	// subresource. False for cooldown and hysteresis paths.
	ShouldPatch bool
}

// ApplyCapAndCooldown implements design §5 steps 6-8:
//  1. step cap (maxStepSize) — emit step_capped_{up,down} tokens
//  2. cooldown gate — overrides step cap; emit cooldown_holding_{up,down}
//     and zero out the patch
//  3. hysteresis — when target == current, emit no_change with no patch
//
// The cooldown-overrides-step-cap precedence is critical for design parity:
// a request that's both clipped and cool-down-blocked must surface as
// cooldown_holding_*, never step_capped_*.
func ApplyCapAndCooldown(in CapInput) CapOutput {
	target := in.Recommended
	reason := reasoning.NoChange

	switch {
	case target > in.Current:
		if target > in.Current+in.MaxStep {
			target = in.Current + in.MaxStep
			reason = reasoning.StepCappedUp
		} else {
			reason = reasoning.ScaleUp
		}
		// Cooldown overrides step cap; zero out the patch.
		if in.Now.Sub(in.LastScaleUp) < time.Duration(in.CooldownUp)*time.Second {
			target = in.Current
			reason = reasoning.CooldownHoldingUp
		}
	case target < in.Current:
		if target < in.Current-in.MaxStep {
			target = in.Current - in.MaxStep
			reason = reasoning.StepCappedDown
		} else {
			reason = reasoning.ScaleDown
		}
		if in.Now.Sub(in.LastScaleDown) < time.Duration(in.CooldownDown)*time.Second {
			target = in.Current
			reason = reasoning.CooldownHoldingDown
		}
	default:
		target = in.Current
		reason = reasoning.NoChange
	}

	return CapOutput{
		Target:      target,
		Reason:      reason,
		ShouldPatch: target != in.Current,
	}
}

// ShouldUpdateRpsPerPod implements the steady-state gate (design §5 step 5):
// only fold a new observation into the ring buffer when:
//   - current_rps >= 10 (avoid noise from low-traffic moments)
//   - replicas >= 1 (otherwise division explodes)
//   - now - lastScale >= 2 * interval (system is past the transient
//     immediately following a scale event)
func ShouldUpdateRpsPerPod(currentRPS float64, replicas int32, lastScale, now time.Time, interval time.Duration) bool {
	if currentRPS < 10 || replicas < 1 {
		return false
	}
	return now.Sub(lastScale) >= 2*interval
}

// ClampRpsPerPod clamps rps_per_pod within [min, max] per design §5 step 5
// "self-calibrating with safety guards".
func ClampRpsPerPod(v float64, minRpsPerPod, maxRpsPerPod int32) float64 {
	if v < float64(minRpsPerPod) {
		return float64(minRpsPerPod)
	}
	if v > float64(maxRpsPerPod) {
		return float64(maxRpsPerPod)
	}
	return v
}

// StatusSeed holds values read from the CR status for restart recovery
// (design §5 step 5 "first-time-seeing-this-CR initialisation").
type StatusSeed struct {
	// RpsPerPodCurrent is the persisted rps_per_pod from the previous
	// controller incarnation. Zero or out-of-bounds means "no usable seed".
	RpsPerPodCurrent float64
	// InBounds is true if RpsPerPodCurrent is within [min, max].
	InBounds bool
	// Midpoint is (min + max) / 2, used as the initial estimate when no
	// usable persisted value is available.
	Midpoint      float64
	LastScaleTime time.Time
}

// InitializeFromStatus seeds a PerCRState from the CR's persisted status.
// When the persisted rpsPerPodCurrent is in-bounds and non-zero, we seed
// the ring buffer with one observation so the controller doesn't have to
// rebuild the median from scratch. Otherwise we accept the midpoint as a
// neutral starting estimate but leave the ring buffer empty (the next
// in-window observation will populate it).
func InitializeFromStatus(state *PerCRState, seed StatusSeed) {
	if seed.InBounds && seed.RpsPerPodCurrent > 0 {
		state.RpsPerPod = seed.RpsPerPodCurrent
		state.Observations.Seed(seed.RpsPerPodCurrent)
	} else {
		state.RpsPerPod = seed.Midpoint
	}
	if !seed.LastScaleTime.IsZero() {
		state.LastScaleUpTime = seed.LastScaleTime
		state.LastScaleDownTime = seed.LastScaleTime
	}
	state.Initialized = true
}
