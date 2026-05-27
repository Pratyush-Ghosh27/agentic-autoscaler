/*
Copyright 2026.
*/

package decision_test

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/decision"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/reasoning"
)

func ptr32(v int32) *int32    { return &v }
func ptrStr(s string) *string { return &s }

// -----------------------------------------------------------------------
// ResolveEffectiveParams — design §5 preamble
// -----------------------------------------------------------------------

func TestResolveEffectiveParams_SpecOverridesAll(t *testing.T) {
	p := decision.ResolveEffectiveParams(decision.ParamSources{
		Spec: decision.SpecParams{
			ScaleUpCooldown:     ptr32(30),
			ScaleDownCooldown:   ptr32(90),
			MaxStepSize:         ptr32(2),
			PreferredForecaster: ptrStr("prophet"),
		},
		Classified: &decision.ClassifiedParams{
			ScaleUpCooldown:     60,
			ScaleDownCooldown:   300,
			MaxStepSize:         3,
			PreferredForecaster: "linear_extrap",
		},
		Defaults: decision.DefaultParams{
			ScaleUpCooldown:   60,
			ScaleDownCooldown: 300,
			MaxStepSize:       4,
		},
	})
	assert.Equal(t, int32(30), p.CooldownUp)
	assert.Equal(t, int32(90), p.CooldownDown)
	assert.Equal(t, int32(2), p.MaxStep)
	assert.Equal(t, "prophet", p.Forecaster)
}

func TestResolveEffectiveParams_ClassifiedFallback(t *testing.T) {
	p := decision.ResolveEffectiveParams(decision.ParamSources{
		Spec: decision.SpecParams{},
		Classified: &decision.ClassifiedParams{
			ScaleUpCooldown:     120,
			ScaleDownCooldown:   180,
			MaxStepSize:         1,
			PreferredForecaster: "prophet",
		},
		Defaults: decision.DefaultParams{
			ScaleUpCooldown:   60,
			ScaleDownCooldown: 300,
			MaxStepSize:       4,
		},
	})
	assert.Equal(t, int32(120), p.CooldownUp)
	assert.Equal(t, int32(180), p.CooldownDown)
	assert.Equal(t, int32(1), p.MaxStep)
	assert.Equal(t, "prophet", p.Forecaster)
}

func TestResolveEffectiveParams_DefaultsFallback(t *testing.T) {
	p := decision.ResolveEffectiveParams(decision.ParamSources{
		Spec:       decision.SpecParams{},
		Classified: nil,
		Defaults: decision.DefaultParams{
			ScaleUpCooldown:   60,
			ScaleDownCooldown: 300,
			MaxStepSize:       4,
		},
	})
	assert.Equal(t, int32(60), p.CooldownUp)
	assert.Equal(t, int32(300), p.CooldownDown)
	assert.Equal(t, int32(4), p.MaxStep)
	assert.Equal(t, "auto", p.Forecaster, "'auto' when nothing overrides")
}

func TestResolveEffectiveParams_EmptyStringClassifiedFallsThrough(t *testing.T) {
	p := decision.ResolveEffectiveParams(decision.ParamSources{
		Spec: decision.SpecParams{},
		Classified: &decision.ClassifiedParams{
			ScaleUpCooldown:     45,
			PreferredForecaster: "", // empty -> fall through to default
		},
		Defaults: decision.DefaultParams{ScaleUpCooldown: 60, MaxStepSize: 4},
	})
	assert.Equal(t, int32(45), p.CooldownUp)
	assert.Equal(t, "auto", p.Forecaster)
}

func TestResolveEffectiveParams_EmptyStringSpecFallsThrough(t *testing.T) {
	empty := ""
	p := decision.ResolveEffectiveParams(decision.ParamSources{
		Spec: decision.SpecParams{PreferredForecaster: &empty},
		Classified: &decision.ClassifiedParams{
			PreferredForecaster: "linear_extrap",
		},
		Defaults: decision.DefaultParams{},
	})
	assert.Equal(t, "linear_extrap", p.Forecaster,
		"empty spec string treated as absent, classified wins")
}

// -----------------------------------------------------------------------
// ComputeRecommended — design §5 step 5
// -----------------------------------------------------------------------

func TestComputeRecommended_Basic(t *testing.T) {
	cases := []struct {
		name      string
		predicted float64
		rpsPerPod float64
		min, max  int32
		want      int32
	}{
		{"exact fit", 300, 100, 1, 10, 3},
		{"fractional ceil", 301, 100, 1, 10, 4},
		{"clamp to min", 50, 100, 2, 10, 2},
		{"clamp to max", 2000, 100, 1, 5, 5},
		{"zero predicted", 0, 100, 1, 10, 1},
		{"very low rps_per_pod", 100, 1, 1, 100, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decision.ComputeRecommended(tc.predicted, tc.rpsPerPod, tc.min, tc.max)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestComputeRecommended_NonPositiveRpsPerPodFailsToMax(t *testing.T) {
	// rps_per_pod <= 0 is mathematically undefined; we fail safe by
	// scaling out to the maximum.
	assert.Equal(t, int32(10), decision.ComputeRecommended(500, 0, 1, 10))
	assert.Equal(t, int32(10), decision.ComputeRecommended(500, -3, 1, 10))
}

// -----------------------------------------------------------------------
// ComputeUnboundedRecommended + ClampRecommended — design §5 step 5
// (the G13 split: unbounded value first, clamp + binding reason second).
// -----------------------------------------------------------------------

func TestComputeUnboundedRecommended_TrivialCases(t *testing.T) {
	t.Run("zero rps per pod returns sentinel", func(t *testing.T) {
		got := decision.ComputeUnboundedRecommended(100.0, 0.0)
		assert.Equal(t, int32(math.MaxInt32), got,
			"unbounded math is undefined at rpsPerPod=0; sentinel surfaces it")
	})
	t.Run("negative rps per pod returns sentinel", func(t *testing.T) {
		got := decision.ComputeUnboundedRecommended(100.0, -1.0)
		assert.Equal(t, int32(math.MaxInt32), got)
	})
	t.Run("ceiling rounds up", func(t *testing.T) {
		got := decision.ComputeUnboundedRecommended(101.0, 10.0)
		assert.Equal(t, int32(11), got)
	})
	t.Run("exact multiple no ceiling overshoot", func(t *testing.T) {
		got := decision.ComputeUnboundedRecommended(100.0, 10.0)
		assert.Equal(t, int32(10), got)
	})
}

func TestClampRecommended_NoBindingWhenInsideRange(t *testing.T) {
	clamped, reason := decision.ClampRecommended(7, 2, 10)
	assert.Equal(t, int32(7), clamped)
	assert.Equal(t, "", reason,
		"in-range recommendation must not set a binding reason")
}

func TestClampRecommended_MaxBinding(t *testing.T) {
	clamped, reason := decision.ClampRecommended(15, 2, 10)
	assert.Equal(t, int32(10), clamped)
	assert.Equal(t, "max_replicas_binding", reason)
}

func TestClampRecommended_MinBinding(t *testing.T) {
	clamped, reason := decision.ClampRecommended(1, 2, 10)
	assert.Equal(t, int32(2), clamped)
	assert.Equal(t, "min_replicas_binding", reason)
}

func TestClampRecommended_SentinelClampsToMax(t *testing.T) {
	// rpsPerPod=0 path: ComputeUnboundedRecommended returned math.MaxInt32;
	// clamp must still produce a valid replica count and surface MaxBinding.
	clamped, reason := decision.ClampRecommended(math.MaxInt32, 2, 10)
	assert.Equal(t, int32(10), clamped)
	assert.Equal(t, "max_replicas_binding", reason)
}

// -----------------------------------------------------------------------
// ApplyCapAndCooldown — design §5 steps 6-8
// -----------------------------------------------------------------------

func TestApplyCapAndCooldown(t *testing.T) {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	longAgo := now.Add(-1 * time.Hour)

	cases := []struct {
		name        string
		in          decision.CapInput
		wantTarget  int32
		wantReason  string
		wantPatched bool
	}{
		{
			name: "scale up within cap and cooldown",
			in: decision.CapInput{
				Recommended: 6, Current: 4, MaxStep: 4,
				CooldownUp: 60, CooldownDown: 300,
				LastScaleUp: longAgo, LastScaleDown: longAgo, Now: now,
			},
			wantTarget: 6, wantReason: reasoning.ScaleUp, wantPatched: true,
		},
		{
			name: "scale up capped by maxStepSize",
			in: decision.CapInput{
				Recommended: 10, Current: 4, MaxStep: 2,
				CooldownUp: 60, CooldownDown: 300,
				LastScaleUp: longAgo, LastScaleDown: longAgo, Now: now,
			},
			wantTarget: 6, wantReason: reasoning.StepCappedUp, wantPatched: true,
		},
		{
			name: "scale up blocked by cooldown",
			in: decision.CapInput{
				Recommended: 6, Current: 4, MaxStep: 4,
				CooldownUp: 60, CooldownDown: 300,
				LastScaleUp: now.Add(-30 * time.Second), LastScaleDown: longAgo, Now: now,
			},
			wantTarget: 4, wantReason: reasoning.CooldownHoldingUp, wantPatched: false,
		},
		{
			name: "cap + cooldown: cooldown wins (no patch)",
			in: decision.CapInput{
				Recommended: 10, Current: 4, MaxStep: 2,
				CooldownUp: 60, CooldownDown: 300,
				LastScaleUp: now.Add(-30 * time.Second), LastScaleDown: longAgo, Now: now,
			},
			wantTarget: 4, wantReason: reasoning.CooldownHoldingUp, wantPatched: false,
		},
		{
			name: "scale down within cap and cooldown",
			in: decision.CapInput{
				Recommended: 3, Current: 5, MaxStep: 4,
				CooldownUp: 60, CooldownDown: 300,
				LastScaleUp: longAgo, LastScaleDown: longAgo, Now: now,
			},
			wantTarget: 3, wantReason: reasoning.ScaleDown, wantPatched: true,
		},
		{
			name: "scale down capped",
			in: decision.CapInput{
				Recommended: 1, Current: 5, MaxStep: 2,
				CooldownUp: 60, CooldownDown: 300,
				LastScaleUp: longAgo, LastScaleDown: longAgo, Now: now,
			},
			wantTarget: 3, wantReason: reasoning.StepCappedDown, wantPatched: true,
		},
		{
			name: "scale down blocked by cooldown",
			in: decision.CapInput{
				Recommended: 3, Current: 5, MaxStep: 4,
				CooldownUp: 60, CooldownDown: 300,
				LastScaleUp: longAgo, LastScaleDown: now.Add(-100 * time.Second), Now: now,
			},
			wantTarget: 5, wantReason: reasoning.CooldownHoldingDown, wantPatched: false,
		},
		{
			name: "no change (hysteresis)",
			in: decision.CapInput{
				Recommended: 5, Current: 5, MaxStep: 4,
				CooldownUp: 60, CooldownDown: 300,
				LastScaleUp: longAgo, LastScaleDown: longAgo, Now: now,
			},
			wantTarget: 5, wantReason: reasoning.NoChange, wantPatched: false,
		},
		{
			name: "cap + cooldown scale-down: cooldown wins (no patch)",
			in: decision.CapInput{
				Recommended: 1, Current: 5, MaxStep: 2,
				CooldownUp: 60, CooldownDown: 300,
				LastScaleUp: longAgo, LastScaleDown: now.Add(-100 * time.Second), Now: now,
			},
			wantTarget: 5, wantReason: reasoning.CooldownHoldingDown, wantPatched: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := decision.ApplyCapAndCooldown(tc.in)
			assert.Equal(t, tc.wantTarget, out.Target, "target")
			assert.Equal(t, tc.wantReason, out.Reason, "reasoning token")
			assert.Equal(t, tc.wantPatched, out.ShouldPatch, "should patch")
		})
	}
}

// -----------------------------------------------------------------------
// ShouldUpdateRpsPerPod — design §5 step 5 steady-state gate
// -----------------------------------------------------------------------

func TestShouldUpdateRpsPerPod(t *testing.T) {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	interval := 60 * time.Second

	cases := []struct {
		name       string
		currentRPS float64
		replicas   int32
		lastScale  time.Time
		want       bool
	}{
		{"steady state", 1000, 5, now.Add(-5 * time.Minute), true},
		{"recently scaled", 1000, 5, now.Add(-90 * time.Second), false},
		{"low RPS", 5, 5, now.Add(-5 * time.Minute), false},
		{"zero replicas", 1000, 0, now.Add(-5 * time.Minute), false},
		{"exactly 2x interval boundary", 1000, 5, now.Add(-2 * interval), true},
		{"just under 2x interval", 1000, 5, now.Add(-2*interval + 1*time.Second), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decision.ShouldUpdateRpsPerPod(tc.currentRPS, tc.replicas, tc.lastScale, now, interval)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestShouldUpdateRpsPerPodWithFloor pins F23: the noise floor on
// the rps_per_pod steady-state gate is now a per-deployment knob, not
// a hard-coded `10`. The default 10 stays available via the
// single-arg ShouldUpdateRpsPerPod for backward compat (legacy
// callers); new callers use the explicit floor variant and read it
// from config.RpsPerPodNoiseFloorRPS (T14 wires the controller).
func TestShouldUpdateRpsPerPodWithFloor(t *testing.T) {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	interval := 60 * time.Second
	lastScale := now.Add(-5 * time.Minute) // outside the 2× window

	cases := []struct {
		name       string
		currentRPS float64
		floor      float64
		want       bool
	}{
		{"7 rps with floor=5 → accepted", 7, 5, true},
		{"7 rps with floor=10 → rejected", 7, 10, false},
		{"exact equality at floor → accepted", 10, 10, true},
		{"just below floor → rejected", 9.99, 10, false},
		{"floor=0 accepts any positive RPS", 0.01, 0, true},
		{"floor=0 still rejects zero RPS", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decision.ShouldUpdateRpsPerPodWithFloor(
				tc.currentRPS, 2, lastScale, now, interval, tc.floor)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestShouldUpdateRpsPerPod_DelegatesToWithFloor pins that the
// single-arg helper preserves the legacy 10-rps floor semantics so
// existing callers (and tests) don't drift.
func TestShouldUpdateRpsPerPod_DelegatesToWithFloor(t *testing.T) {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	interval := 60 * time.Second
	lastScale := now.Add(-5 * time.Minute)
	// 9.5 rps: below the legacy floor → must be rejected.
	got := decision.ShouldUpdateRpsPerPod(9.5, 2, lastScale, now, interval)
	assert.False(t, got, "single-arg helper must keep the legacy 10-rps floor")
	// 10.5 rps: above the legacy floor → must be accepted.
	got = decision.ShouldUpdateRpsPerPod(10.5, 2, lastScale, now, interval)
	assert.True(t, got)
}

// -----------------------------------------------------------------------
// ClampRpsPerPod
// -----------------------------------------------------------------------

func TestClampRpsPerPod(t *testing.T) {
	assert.InDelta(t, 50.0, decision.ClampRpsPerPod(30, 50, 500), 0.001)
	assert.InDelta(t, 500.0, decision.ClampRpsPerPod(600, 50, 500), 0.001)
	assert.InDelta(t, 200.0, decision.ClampRpsPerPod(200, 50, 500), 0.001)
	assert.InDelta(t, 50.0, decision.ClampRpsPerPod(50, 50, 500), 0.001, "min boundary")
	assert.InDelta(t, 500.0, decision.ClampRpsPerPod(500, 50, 500), 0.001, "max boundary")
}

// -----------------------------------------------------------------------
// InitializeFromStatus — design §5 step 5 restart recovery
// -----------------------------------------------------------------------

func TestInitializeState_FromStatus(t *testing.T) {
	state := &decision.PerCRState{Observations: decision.NewRingBuffer(10)}
	lastScale := time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC)
	decision.InitializeFromStatus(state, decision.StatusSeed{
		RpsPerPodCurrent: 175,
		InBounds:         true,
		LastScaleTime:    lastScale,
	})
	assert.True(t, state.Initialized)
	assert.InDelta(t, 175.0, state.RpsPerPod, 0.001)
	assert.Len(t, state.Observations.Values(), 1)
	assert.Equal(t, lastScale, state.LastScaleUpTime)
	assert.Equal(t, lastScale, state.LastScaleDownTime)
}

func TestInitializeState_Midpoint(t *testing.T) {
	state := &decision.PerCRState{Observations: decision.NewRingBuffer(10)}
	decision.InitializeFromStatus(state, decision.StatusSeed{
		RpsPerPodCurrent: 0,
		InBounds:         false,
		Midpoint:         275,
	})
	assert.True(t, state.Initialized)
	assert.InDelta(t, 275.0, state.RpsPerPod, 0.001)
	assert.Empty(t, state.Observations.Values(), "no seeding when OOB")
}

func TestInitializeState_OutOfBoundsButPositive(t *testing.T) {
	// A non-zero persisted value that's reported as out-of-bounds (e.g.
	// admin tightened rpsPerPod{Min,Max}) should fall through to the
	// midpoint, not seed the ring buffer.
	state := &decision.PerCRState{Observations: decision.NewRingBuffer(10)}
	decision.InitializeFromStatus(state, decision.StatusSeed{
		RpsPerPodCurrent: 700,
		InBounds:         false,
		Midpoint:         275,
	})
	assert.InDelta(t, 275.0, state.RpsPerPod, 0.001)
	assert.Empty(t, state.Observations.Values())
}

func TestInitializeState_NoLastScaleTime(t *testing.T) {
	state := &decision.PerCRState{Observations: decision.NewRingBuffer(10)}
	decision.InitializeFromStatus(state, decision.StatusSeed{
		RpsPerPodCurrent: 100,
		InBounds:         true,
		// LastScaleTime intentionally zero — first reconcile of a fresh CR.
	})
	assert.True(t, state.LastScaleUpTime.IsZero())
	assert.True(t, state.LastScaleDownTime.IsZero())
}
