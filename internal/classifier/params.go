/*
Copyright 2026.
*/

package classifier

import "math"

// Parameter formula constants from design §7. These names match the
// table in the design doc 1:1; do not rename.
const (
	BaseScaleUpCooldown        = 120.0
	KCVUp                      = 2.0
	ScaleUpCooldownHardFloor   = int32(30)
	ScaleUpCooldownHardCeiling = int32(180)

	BaseScaleDownCooldown        = 180.0
	KCVDown                      = 1.5
	KTodDown                     = 0.5
	ScaleDownCooldownHardFloor   = int32(60)
	ScaleDownCooldownHardCeiling = int32(600)

	// PreferredForecaster thresholds: prefer prophet when there's enough
	// signal for it to beat linear extrapolation. Sustained periodicity
	// or a strong trend qualifies; pure noise does not.
	prophetTodCorrelationAbove = 0.70
	prophetTrendSlopeAbove     = 2.0
)

// Forecaster names. Match the CRD enum values exactly.
const (
	ForecasterLinearExtrap = "linear_extrap"
	ForecasterProphet      = "prophet"
)

// ClassifiedOutput is the set of params the classifier writes to
// status.classifiedParams. Field names mirror the CRD type's fields
// (without the trailing "Seconds" on cooldowns — translation happens at
// the worker patch site).
type ClassifiedOutput struct {
	ScaleUpCooldown     int32
	ScaleDownCooldown   int32
	MaxStep             int32
	PreferredForecaster string
}

// ComputeParams applies the design §7 formulae to produce recommended
// scaling params from the extracted features.
//
// Formulae:
//
//	scaleUpCooldown   = clamp(round(BASE_UP / (1 + K_CV_UP*cv)), floor, ceiling)
//	scaleDownCooldown = clamp(round(BASE_DOWN * (1 + K_CV_DOWN*cv)
//	                          / (1 + K_TOD_DOWN*max(0, tod))), floor, ceiling)
//	maxStep           = clamp(ceil(log2(peak_to_trough)), 1, max-min)
//	preferredForecaster = "prophet" when tod>0.70 OR |trend|>2.0
//	                      "linear_extrap" otherwise
//
// minReplicas/maxReplicas come from the CRD spec (or its defaults) — they
// bound maxStep so that a single reconcile cannot cross the entire range.
func ComputeParams(f Features, minReplicas, maxReplicas int32) ClassifiedOutput {
	rawUp := BaseScaleUpCooldown / (1 + KCVUp*f.CV)
	scaleUp := clampInt32(int32(math.Round(rawUp)),
		ScaleUpCooldownHardFloor, ScaleUpCooldownHardCeiling)

	todFactor := math.Max(0, f.TodCorrelation)
	rawDown := BaseScaleDownCooldown * (1 + KCVDown*f.CV) / (1 + KTodDown*todFactor)
	scaleDown := clampInt32(int32(math.Round(rawDown)),
		ScaleDownCooldownHardFloor, ScaleDownCooldownHardCeiling)

	var maxStep int32
	if f.PeakToTrough <= 1 {
		maxStep = 1
	} else {
		maxStep = int32(math.Ceil(math.Log2(f.PeakToTrough)))
	}
	replicaRange := maxReplicas - minReplicas
	if replicaRange < 1 {
		replicaRange = 1
	}
	maxStep = clampInt32(maxStep, 1, replicaRange)

	forecaster := ForecasterLinearExtrap
	if f.TodCorrelation > prophetTodCorrelationAbove ||
		math.Abs(f.TrendSlope) > prophetTrendSlopeAbove {
		forecaster = ForecasterProphet
	}

	return ClassifiedOutput{
		ScaleUpCooldown:     scaleUp,
		ScaleDownCooldown:   scaleDown,
		MaxStep:             maxStep,
		PreferredForecaster: forecaster,
	}
}

func clampInt32(v, minVal, maxVal int32) int32 {
	if v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}
