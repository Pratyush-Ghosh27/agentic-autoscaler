/*
Copyright 2026.
*/

package classifier

import "math"

// Pattern names. Stable wire format — Grafana panels and the
// AgenticAutoscaler CRD enum match on these strings. Renaming any of
// them is a breaking change.
const (
	PatternFlat        = "flat"
	PatternPeriodic    = "periodic"
	PatternSpiky       = "spiky"
	PatternGradualRamp = "gradual_ramp"
	PatternDefault     = "default"
)

// Classification thresholds. From design_v2.md §7. Any change here
// must be mirrored in CONTEXT.md / runbooks because operators tune by
// them.
const (
	cvFlatBelow            = 0.10
	tdCorrelationAbove     = 0.70
	cvSpikyAbove           = 0.50
	peakToTroughSpikyAbove = 5.0

	// GradualRampDailyDriftFrac is the relative-drift threshold for
	// the gradual_ramp rule. The rule fires when
	//
	//	|slope| * 1440 / max(mean, 1) > GradualRampDailyDriftFrac
	//
	// i.e. when the projected 24-hour drift exceeds 20% of the
	// workload's mean RPS. Replaces the v1 absolute |slope| > 2.0
	// rule, which silently misbehaved at very high or very low mean
	// (a 2 rps/min slope is a 50% drift on a 60-rps service but only
	// a 0.1% drift on a 30-krps service). F26 + G11.
	GradualRampDailyDriftFrac = 0.20
)

// Classify is the single-argument convenience wrapper. It calls
// ClassifyWithMean with mean=1.0, so its gradual_ramp branch fires for
// any |slope| > GradualRampDailyDriftFrac/1440 (~1.4e-4). Production
// code should call ClassifyWithMean(f, realMean); this helper exists
// for unit tests, debugging tools, and pre-context (cold-start)
// callers that don't yet have a mean to supply.
func Classify(f Features) string {
	return ClassifyWithMean(f, 1.0)
}

// ClassifyWithMean applies the priority-ordered pattern rules from
// design_v2.md §7 with the gradual_ramp threshold scaled by the
// workload's mean RPS. First match wins. The order matters: a
// workload that's both periodic AND high-cv must classify as periodic
// (the cycle dominates the noise). F26 + G11.
func ClassifyWithMean(f Features, seriesMean float64) string {
	denom := math.Max(seriesMean, 1.0)
	switch {
	case f.CV < cvFlatBelow:
		return PatternFlat
	case f.TodCorrelation > tdCorrelationAbove:
		return PatternPeriodic
	case f.CV > cvSpikyAbove && f.PeakToTrough > peakToTroughSpikyAbove:
		return PatternSpiky
	case math.Abs(f.TrendSlope)*1440/denom > GradualRampDailyDriftFrac:
		return PatternGradualRamp
	default:
		return PatternDefault
	}
}
