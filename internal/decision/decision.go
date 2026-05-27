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
// quirk of design_v2.md §5 testable from a Go unit test in microseconds.
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

// ComputeUnboundedRecommended returns the raw forecaster-driven replica count,
// ceil(predictedRPS / rpsPerPod), with no CRD-bound clamp. When rpsPerPod is
// non-positive the result is math.MaxInt32 — a sentinel that subsequent
// ClampRecommended turns into maxReplicas (the failsafe). Surfacing the
// sentinel (rather than silently returning maxReplicas here) lets callers
// distinguish "forecast over the cap" from "no usable rps_per_pod".
func ComputeUnboundedRecommended(predictedRPS, rpsPerPod float64) int32 {
	if rpsPerPod <= 0 {
		return math.MaxInt32
	}
	return int32(math.Ceil(predictedRPS / rpsPerPod))
}

// ClampRecommended applies the CRD bounds and reports which bound (if any)
// was the binding constraint. The returned reasoning string is one of:
//   - "max_replicas_binding"  when unbounded > maxReplicas (clamped to max)
//   - "min_replicas_binding"  when unbounded < minReplicas (clamped to min)
//   - ""                       when unbounded is already in [min, max]
//
// Per design §5 precedence rule 1, this binding reason is *tentative* —
// step 6 (cap) and step 7 (cooldown) may overwrite it in ApplyCapAndCooldown.
// The string literals are duplicated from reasoning.MaxReplicasBinding /
// MinReplicasBinding to avoid importing reasoning into decision (decision
// is the lower-level package). The tokens_test snapshot pins them in sync.
func ClampRecommended(unbounded, minReplicas, maxReplicas int32) (int32, string) {
	if unbounded > maxReplicas {
		return maxReplicas, "max_replicas_binding"
	}
	if unbounded < minReplicas {
		return minReplicas, "min_replicas_binding"
	}
	return unbounded, ""
}

// CapInput feeds ApplyCapAndCooldown.
type CapInput struct {
	Recommended   int32 // post-clamp value from ClampRecommended
	Current       int32
	MaxStep       int32
	CooldownUp    int32 // seconds
	CooldownDown  int32 // seconds
	LastScaleUp   time.Time
	LastScaleDown time.Time
	Now           time.Time
	// BindingReason is the tentative reasoning token set by step 5
	// (decision.ClampRecommended). One of reasoning.MaxReplicasBinding,
	// reasoning.MinReplicasBinding, or "". Step 6 (cap) and step 7
	// (cooldown) overwrite it when those constraints also fire. Step 8
	// hysteresis preserves it — the binding-without-replica-change branch.
	BindingReason string
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

// ApplyCapAndCooldown implements design_v2.md §5 steps 6-8 with the
// precedence chain from §5 precedence rules 1-4:
//
//  1. Start with the tentative binding reason from step 5
//     (in.BindingReason, set by ClampRecommended).
//  2. Step cap (maxStepSize) — overwrites with step_capped_{up,down}
//     when the cap clips the move.
//  3. Cooldown — overwrites with cooldown_holding_{up,down} when it
//     blocks the move entirely.
//  4. Hysteresis (target == current) — preserves the binding reason if
//     any is still set, else emits no_change.
//
// When target moves (case target > current or target < current), the
// reason is always overwritten by step 6 / 7 / scale_{up,down}; the
// binding reason in those branches is implicitly surfaced via the event
// message's unboundedRecommended field (recorded in step 10).
func ApplyCapAndCooldown(in CapInput) CapOutput {
	target := in.Recommended
	reason := in.BindingReason

	switch {
	case target > in.Current:
		if target > in.Current+in.MaxStep {
			target = in.Current + in.MaxStep
			reason = reasoning.StepCappedUp
		} else {
			reason = reasoning.ScaleUp
		}
		// Cooldown overrides step cap and binding reason; zero out the patch.
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
		// target == current: preserve binding reason if set, else no_change.
		target = in.Current
		if reason == "" {
			reason = reasoning.NoChange
		}
	}

	return CapOutput{
		Target:      target,
		Reason:      reason,
		ShouldPatch: target != in.Current,
	}
}

// DefaultRpsPerPodNoiseFloor is the legacy single-arg
// ShouldUpdateRpsPerPod's noise floor (10 rps). New callers should
// pass an explicit floor via ShouldUpdateRpsPerPodWithFloor and read
// it from config.RpsPerPodNoiseFloorRPS so the threshold can be
// tuned per-deployment. F23.
const DefaultRpsPerPodNoiseFloor = 10.0

// ShouldUpdateRpsPerPod is the legacy single-arg gate. Equivalent to
// ShouldUpdateRpsPerPodWithFloor with floor = DefaultRpsPerPodNoiseFloor.
// Preserved for callers that haven't been migrated; T14 switches the
// controller reconciler to the explicit-floor variant.
func ShouldUpdateRpsPerPod(currentRPS float64, replicas int32, lastScale, now time.Time, interval time.Duration) bool {
	return ShouldUpdateRpsPerPodWithFloor(
		currentRPS, replicas, lastScale, now, interval, DefaultRpsPerPodNoiseFloor)
}

// ShouldUpdateRpsPerPodWithFloor implements the steady-state gate
// (design §5 step 5) with a configurable noise floor. Only fold a new
// observation into the ring buffer when:
//   - current_rps >= noiseFloor (avoid noise from low-traffic moments;
//     a noiseFloor of 0 effectively disables the check)
//   - replicas >= 1 (otherwise division explodes)
//   - now - lastScale >= 2 * interval (system is past the transient
//     immediately following a scale event)
//
// F23 made the floor tunable per deployment via config.Config's
// RpsPerPodNoiseFloorRPS; the controller reconciler reads from there
// and threads it into this call (wired in T14).
func ShouldUpdateRpsPerPodWithFloor(
	currentRPS float64,
	replicas int32,
	lastScale, now time.Time,
	interval time.Duration,
	noiseFloor float64,
) bool {
	if currentRPS <= 0 || replicas < 1 {
		return false
	}
	if currentRPS < noiseFloor {
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

// RestartSeedCopies is the number of ring-buffer copies used when
// reseeding from persisted status on controller restart. Five copies
// (vs. one) keep the operator's runtime calibration dominant in the
// median across the next 5+ observations — without this, the persisted
// value is washed out by the second new sample and a misguided scale
// decision can fire on a wobbly median during the post-restart window.
// See design_v2.md F20.
const RestartSeedCopies = 5

// InitializeFromStatus seeds a PerCRState from the CR's persisted status.
// When the persisted rpsPerPodCurrent is in-bounds and non-zero, we seed
// the ring buffer with RestartSeedCopies copies (F20) so the controller
// doesn't have to rebuild the median from scratch and the persisted
// estimate dominates the median through the next several observations.
// Otherwise we accept the midpoint as a neutral starting estimate but
// leave the ring buffer empty (the next in-window observation will
// populate it).
func InitializeFromStatus(state *PerCRState, seed StatusSeed) {
	if seed.InBounds && seed.RpsPerPodCurrent > 0 {
		state.RpsPerPod = seed.RpsPerPodCurrent
		state.Observations.SeedN(seed.RpsPerPodCurrent, RestartSeedCopies)
	} else {
		state.RpsPerPod = seed.Midpoint
	}
	if !seed.LastScaleTime.IsZero() {
		state.LastScaleUpTime = seed.LastScaleTime
		state.LastScaleDownTime = seed.LastScaleTime
	}
	state.Initialized = true
}
