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

	BaseScaleDownCooldown = 180.0
	KCVDown               = 1.5
	// KPeriodicDown was named KTodDown in v1 (when the feature was
	// `tod_correlation`). v2 generalised the feature to
	// `hourly_autocorr` and the user-visible pattern label to
	// `periodic`, so the constant is renamed for symmetry. See F13
	// and design_v2.md §7.
	KPeriodicDown                = 0.5
	ScaleDownCooldownHardFloor   = int32(60)
	ScaleDownCooldownHardCeiling = int32(600)
)

// Forecaster names. Match the CRD enum values exactly.
const (
	ForecasterLinearExtrap = "linear_extrap"
	ForecasterProphet      = "prophet"
	// ForecasterGBDTQuantile is the LightGBM quantile-regression
	// forecaster for spiky workloads. Phase 3 / G12; see
	// docs/design_v2.md §5 forecast_gbdt_quantile.
	ForecasterGBDTQuantile = "gbdt_quantile"
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

// ComputeParams applies the design_v2 §7 formulae to produce recommended
// scaling params from the extracted features and the classifier pattern.
//
// Formulae:
//
//	scaleUpCooldown   = clamp(round(BASE_UP / (1 + K_CV_UP*cv)), floor, ceiling)
//	scaleDownCooldown = clamp(round(BASE_DOWN * (1 + K_CV_DOWN*cv)
//	                          / (1 + K_PERIODIC_DOWN*max(0, tod))), floor, ceiling)
//	maxStep           = clamp(ceil(log2(peak_to_trough)), 1, max-min)
//	preferredForecaster = pattern -> forecaster table (G19, see below).
//
// pattern -> forecaster table (G19, Phase 3):
//
//	flat / gradual_ramp / default  -> linear_extrap
//	periodic                       -> prophet
//	spiky                          -> gbdt_quantile
//	(anything else)                -> linear_extrap (safe fallback)
//
// minReplicas/maxReplicas come from the CRD spec (or its defaults) — they
// bound maxStep so that a single reconcile cannot cross the entire range.
//
// Breaking change in Phase 3: the legacy v1 feature-driven selector
// (`prophet when tod>0.70 OR |trend|>2.0`) is gone. Pattern is the
// single source of truth so the controller, the worker, and the
// Forecast Service all see the same routing decision.
func ComputeParams(
	f Features,
	pattern string,
	minReplicas, maxReplicas int32,
) ClassifiedOutput {
	rawUp := BaseScaleUpCooldown / (1 + KCVUp*f.CV)
	scaleUp := clampInt32(int32(math.Round(rawUp)),
		ScaleUpCooldownHardFloor, ScaleUpCooldownHardCeiling)

	todFactor := math.Max(0, f.TodCorrelation)
	rawDown := BaseScaleDownCooldown * (1 + KCVDown*f.CV) / (1 + KPeriodicDown*todFactor)
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

	return ClassifiedOutput{
		ScaleUpCooldown:     scaleUp,
		ScaleDownCooldown:   scaleDown,
		MaxStep:             maxStep,
		PreferredForecaster: forecasterForPattern(pattern),
	}
}

// forecasterForPattern is the G19 pattern → forecaster table. Unknown
// inputs (including the empty string) fall back to linear_extrap so
// the safe path is the structural default — gbdt_quantile is only
// reachable via the explicit "spiky" arm, mirroring F22 on the Python
// dispatcher side.
func forecasterForPattern(pattern string) string {
	switch pattern {
	case PatternPeriodic:
		return ForecasterProphet
	case PatternSpiky:
		return ForecasterGBDTQuantile
	case PatternFlat, PatternGradualRamp, PatternDefault:
		return ForecasterLinearExtrap
	default:
		return ForecasterLinearExtrap
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
