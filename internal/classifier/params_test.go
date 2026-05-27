/*
Copyright 2026.
*/

package classifier_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/classifier"
)

// TestKPeriodicDownConstantExists pins F13: the cooldown-down
// multiplier for the periodic pattern is exported as KPeriodicDown,
// matching design_v2.md §7. The legacy KTodDown name is gone.
func TestKPeriodicDownConstantExists(t *testing.T) {
	assert.Equal(t, 0.5, classifier.KPeriodicDown,
		"KPeriodicDown must be 0.5 per design_v2.md §7")
}

// TestForecasterGBDTQuantileConstantExists pins G12 (Phase 3): the
// third forecaster's name is exported as ForecasterGBDTQuantile and
// matches the snake_case value used in the CRD enum, the webhook
// validator, and the Forecast Service Pydantic Literal.
func TestForecasterGBDTQuantileConstantExists(t *testing.T) {
	assert.Equal(t, "gbdt_quantile", classifier.ForecasterGBDTQuantile,
		"ForecasterGBDTQuantile must be %q per docs/design_v2.md §5", "gbdt_quantile")
}

// TestComputeParams_PatternDrivenForecaster pins G19 (Phase 3): the
// PreferredForecaster output is selected from the classifier pattern,
// NOT from feature thresholds. This locks in the Phase 3 selector
// flip: flat / gradual_ramp / default -> linear_extrap, periodic ->
// prophet, spiky -> gbdt_quantile.
func TestComputeParams_PatternDrivenForecaster(t *testing.T) {
	tests := []struct {
		pattern string
		want    string
	}{
		{classifier.PatternFlat, classifier.ForecasterLinearExtrap},
		{classifier.PatternGradualRamp, classifier.ForecasterLinearExtrap},
		{classifier.PatternDefault, classifier.ForecasterLinearExtrap},
		{classifier.PatternPeriodic, classifier.ForecasterProphet},
		{classifier.PatternSpiky, classifier.ForecasterGBDTQuantile},
	}
	// Features values are picked so feature-driven legacy thresholds
	// would have flipped to prophet (tod=0.8 > 0.70, slope=3.0 > 2.0).
	// If the selector is *still* feature-driven, every test case here
	// will land on prophet and the table will fail loudly.
	f := classifier.Features{CV: 0.1, HourlyAutocorr: 0.8, TrendSlope: 3.0, PeakToTrough: 2.0}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			got := classifier.ComputeParams(f, tt.pattern, 1, 10)
			assert.Equal(t, tt.want, got.PreferredForecaster,
				"pattern=%q: forecaster selector must be pattern-driven (G19)",
				tt.pattern)
		})
	}
}

// TestComputeParams_UnknownPatternFallsBackToLinearExtrap pins the
// safety net for G19: an unknown pattern string (e.g. a future
// addition not yet handled by the selector) must NOT panic and must
// NOT silently pick gbdt_quantile — it falls back to linear_extrap
// which is the historically-safe default.
func TestComputeParams_UnknownPatternFallsBackToLinearExtrap(t *testing.T) {
	f := classifier.Features{CV: 0.1, HourlyAutocorr: 0.5, TrendSlope: 0.1, PeakToTrough: 2.0}
	got := classifier.ComputeParams(f, "<future-unknown>", 1, 10)
	assert.Equal(t, classifier.ForecasterLinearExtrap, got.PreferredForecaster,
		"unknown pattern must fall back to linear_extrap, not gbdt_quantile")
}

func TestComputeParams_FlatTraffic(t *testing.T) {
	// cv=0, tod=0 → scaleUp = 120/(1+0) = 120, scaleDown = 180*1/1 = 180.
	// pt=1 → maxStep clamped to 1. pattern=flat → linear_extrap.
	f := classifier.Features{CV: 0, HourlyAutocorr: 0, PeakToTrough: 1, TrendSlope: 0}
	p := classifier.ComputeParams(f, classifier.PatternFlat, 2, 10)
	assert.Equal(t, int32(120), p.ScaleUpCooldown)
	assert.Equal(t, int32(180), p.ScaleDownCooldown)
	assert.Equal(t, int32(1), p.MaxStep)
	assert.Equal(t, classifier.ForecasterLinearExtrap, p.PreferredForecaster)
}

func TestComputeParams_HighCV(t *testing.T) {
	// cv=0.5, tod=0 → scaleUp = round(120/(1+1.0)) = 60.
	// scaleDown = round(180*(1+0.75)/1) = 315.
	// pt=5 → maxStep = ceil(log2(5)) = 3. pattern=default → linear_extrap.
	f := classifier.Features{CV: 0.5, HourlyAutocorr: 0, PeakToTrough: 5, TrendSlope: 0}
	p := classifier.ComputeParams(f, classifier.PatternDefault, 2, 10)
	assert.Equal(t, int32(60), p.ScaleUpCooldown)
	assert.Equal(t, int32(315), p.ScaleDownCooldown)
	assert.Equal(t, int32(3), p.MaxStep)
	assert.Equal(t, classifier.ForecasterLinearExtrap, p.PreferredForecaster)
}

func TestComputeParams_PeriodicTraffic(t *testing.T) {
	// pattern=periodic → prophet.
	// cv=0.2 → scaleDown = round(180*(1+0.3)/(1+0.4)) ≈ 167.
	// pt=3 → maxStep = ceil(log2(3)) = 2.
	f := classifier.Features{CV: 0.2, HourlyAutocorr: 0.8, PeakToTrough: 3, TrendSlope: 0}
	p := classifier.ComputeParams(f, classifier.PatternPeriodic, 2, 10)
	assert.Equal(t, classifier.ForecasterProphet, p.PreferredForecaster)
	assert.InDelta(t, 167, p.ScaleDownCooldown, 1)
	assert.Equal(t, int32(2), p.MaxStep)
}

func TestComputeParams_VeryHighCV_HitsScaleUpFloor(t *testing.T) {
	// cv=2.0 → raw = 120/(1+4) = 24 → clamped to 30 (hard floor).
	f := classifier.Features{CV: 2.0, HourlyAutocorr: 0, PeakToTrough: 10, TrendSlope: 0}
	p := classifier.ComputeParams(f, classifier.PatternDefault, 1, 8)
	assert.Equal(t, classifier.ScaleUpCooldownHardFloor, p.ScaleUpCooldown)
	assert.Equal(t, int32(4), p.MaxStep, "ceil(log2(10)) ≈ 3.32 → 4")
}

func TestComputeParams_HighCV_HitsScaleDownCeiling(t *testing.T) {
	// cv=1.56, tod=0 → raw = 180*(1+2.34)/1 ≈ 601 → clamped to 600.
	f := classifier.Features{CV: 1.56, HourlyAutocorr: 0, PeakToTrough: 2, TrendSlope: 0}
	p := classifier.ComputeParams(f, classifier.PatternDefault, 2, 10)
	assert.Equal(t, classifier.ScaleDownCooldownHardCeiling, p.ScaleDownCooldown)
}

func TestComputeParams_MaxStepClampedToReplicaRange(t *testing.T) {
	// pt=1000 → ceil(log2(1000)) = 10, but maxReplicas-minReplicas = 5.
	f := classifier.Features{CV: 0.6, HourlyAutocorr: 0, PeakToTrough: 1000, TrendSlope: 0}
	p := classifier.ComputeParams(f, classifier.PatternDefault, 3, 8)
	assert.Equal(t, int32(5), p.MaxStep)
}

func TestComputeParams_MinReplicasEqualsMaxReplicas(t *testing.T) {
	// max-min=0 → we still allow at least 1 to avoid a frozen system.
	f := classifier.Features{CV: 0.6, HourlyAutocorr: 0, PeakToTrough: 100, TrendSlope: 0}
	p := classifier.ComputeParams(f, classifier.PatternDefault, 4, 4)
	assert.Equal(t, int32(1), p.MaxStep, "minimum step is 1 even when min == max")
}

// TestComputeParams_GradualRampUsesLinearExtrap is the rename of the
// legacy `TestComputeParams_RampUsesProphet` test. Phase 3 (G19)
// flipped the selector: gradual_ramp now picks linear_extrap because
// a steady ramp is exactly what linear extrapolation handles best.
func TestComputeParams_GradualRampUsesLinearExtrap(t *testing.T) {
	f := classifier.Features{CV: 0.2, HourlyAutocorr: 0.3, PeakToTrough: 2, TrendSlope: 3.0}
	p := classifier.ComputeParams(f, classifier.PatternGradualRamp, 2, 10)
	assert.Equal(t, classifier.ForecasterLinearExtrap, p.PreferredForecaster,
		"gradual_ramp must pick linear_extrap under the pattern-driven selector (G19)")
}

func TestComputeParams_NegativeTodTreatedAsZero(t *testing.T) {
	// tod=-0.5 should not push scaleDownCooldown DOWN — max(0, tod) clamps it.
	fNeg := classifier.Features{CV: 0.2, HourlyAutocorr: -0.5, PeakToTrough: 2, TrendSlope: 0}
	fZero := classifier.Features{CV: 0.2, HourlyAutocorr: 0.0, PeakToTrough: 2, TrendSlope: 0}
	pNeg := classifier.ComputeParams(fNeg, classifier.PatternDefault, 2, 10)
	pZero := classifier.ComputeParams(fZero, classifier.PatternDefault, 2, 10)
	assert.Equal(t, pZero.ScaleDownCooldown, pNeg.ScaleDownCooldown)
}

func TestComputeParams_PeakToTroughBelowOneClampsToOne(t *testing.T) {
	f := classifier.Features{CV: 0.2, HourlyAutocorr: 0, PeakToTrough: 0.5, TrendSlope: 0}
	p := classifier.ComputeParams(f, classifier.PatternDefault, 2, 10)
	assert.Equal(t, int32(1), p.MaxStep)
}
