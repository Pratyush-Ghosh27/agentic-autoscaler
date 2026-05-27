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

func TestComputeParams_FlatTraffic(t *testing.T) {
	// cv=0, tod=0 → scaleUp = 120/(1+0) = 120, scaleDown = 180*1/1 = 180
	// pt=1 → maxStep clamped to 1; tod=0 + slope=0 → linear_extrap.
	f := classifier.Features{CV: 0, TodCorrelation: 0, PeakToTrough: 1, TrendSlope: 0}
	p := classifier.ComputeParams(f, 2, 10)
	assert.Equal(t, int32(120), p.ScaleUpCooldown)
	assert.Equal(t, int32(180), p.ScaleDownCooldown)
	assert.Equal(t, int32(1), p.MaxStep)
	assert.Equal(t, classifier.ForecasterLinearExtrap, p.PreferredForecaster)
}

func TestComputeParams_HighCV(t *testing.T) {
	// cv=0.5, tod=0 → scaleUp = round(120/(1+1.0)) = 60
	// scaleDown = round(180*(1+0.75)/1) = round(315) = 315
	// pt=5 → maxStep = ceil(log2(5)) = 3; tod=0 → linear_extrap.
	f := classifier.Features{CV: 0.5, TodCorrelation: 0, PeakToTrough: 5, TrendSlope: 0}
	p := classifier.ComputeParams(f, 2, 10)
	assert.Equal(t, int32(60), p.ScaleUpCooldown)
	assert.Equal(t, int32(315), p.ScaleDownCooldown)
	assert.Equal(t, int32(3), p.MaxStep)
	assert.Equal(t, classifier.ForecasterLinearExtrap, p.PreferredForecaster)
}

func TestComputeParams_PeriodicTraffic(t *testing.T) {
	// tod=0.8 → prophet preferred.
	// cv=0.2 → scaleDown = round(180*(1+0.3)/(1+0.4)) = round(180*1.3/1.4) ≈ 167.
	// pt=3 → maxStep = ceil(log2(3)) = 2.
	f := classifier.Features{CV: 0.2, TodCorrelation: 0.8, PeakToTrough: 3, TrendSlope: 0}
	p := classifier.ComputeParams(f, 2, 10)
	assert.Equal(t, classifier.ForecasterProphet, p.PreferredForecaster)
	assert.InDelta(t, 167, p.ScaleDownCooldown, 1)
	assert.Equal(t, int32(2), p.MaxStep)
}

func TestComputeParams_VeryHighCV_HitsScaleUpFloor(t *testing.T) {
	// cv=2.0 → raw = 120/(1+4) = 24 → clamped to 30 (hard floor).
	f := classifier.Features{CV: 2.0, TodCorrelation: 0, PeakToTrough: 10, TrendSlope: 0}
	p := classifier.ComputeParams(f, 1, 8)
	assert.Equal(t, classifier.ScaleUpCooldownHardFloor, p.ScaleUpCooldown)
	assert.Equal(t, int32(4), p.MaxStep, "ceil(log2(10)) ≈ 3.32 → 4")
}

func TestComputeParams_HighCV_HitsScaleDownCeiling(t *testing.T) {
	// cv=1.56, tod=0 → raw = 180*(1+2.34)/1 ≈ 601 → clamped to 600.
	f := classifier.Features{CV: 1.56, TodCorrelation: 0, PeakToTrough: 2, TrendSlope: 0}
	p := classifier.ComputeParams(f, 2, 10)
	assert.Equal(t, classifier.ScaleDownCooldownHardCeiling, p.ScaleDownCooldown)
}

func TestComputeParams_MaxStepClampedToReplicaRange(t *testing.T) {
	// pt=1000 → ceil(log2(1000)) = 10, but maxReplicas-minReplicas = 5.
	f := classifier.Features{CV: 0.6, TodCorrelation: 0, PeakToTrough: 1000, TrendSlope: 0}
	p := classifier.ComputeParams(f, 3, 8)
	assert.Equal(t, int32(5), p.MaxStep)
}

func TestComputeParams_MinReplicasEqualsMaxReplicas(t *testing.T) {
	// max-min=0 → we still allow at least 1 to avoid a frozen system.
	f := classifier.Features{CV: 0.6, TodCorrelation: 0, PeakToTrough: 100, TrendSlope: 0}
	p := classifier.ComputeParams(f, 4, 4)
	assert.Equal(t, int32(1), p.MaxStep, "minimum step is 1 even when min == max")
}

func TestComputeParams_RampUsesProphet(t *testing.T) {
	f := classifier.Features{CV: 0.2, TodCorrelation: 0.3, PeakToTrough: 2, TrendSlope: 3.0}
	p := classifier.ComputeParams(f, 2, 10)
	assert.Equal(t, classifier.ForecasterProphet, p.PreferredForecaster,
		"high |slope| switches to prophet")
}

func TestComputeParams_NegativeTodTreatedAsZero(t *testing.T) {
	// tod=-0.5 should not push scaleDownCooldown DOWN — max(0, tod) clamps it.
	fNeg := classifier.Features{CV: 0.2, TodCorrelation: -0.5, PeakToTrough: 2, TrendSlope: 0}
	fZero := classifier.Features{CV: 0.2, TodCorrelation: 0.0, PeakToTrough: 2, TrendSlope: 0}
	pNeg := classifier.ComputeParams(fNeg, 2, 10)
	pZero := classifier.ComputeParams(fZero, 2, 10)
	assert.Equal(t, pZero.ScaleDownCooldown, pNeg.ScaleDownCooldown)
}

func TestComputeParams_PeakToTroughBelowOneClampsToOne(t *testing.T) {
	f := classifier.Features{CV: 0.2, TodCorrelation: 0, PeakToTrough: 0.5, TrendSlope: 0}
	p := classifier.ComputeParams(f, 2, 10)
	assert.Equal(t, int32(1), p.MaxStep)
}
