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

// Classification thresholds. From design §7. Any change here must be
// mirrored in CONTEXT.md / runbooks because operators tune by them.
const (
	cvFlatBelow            = 0.10
	tdCorrelationAbove     = 0.70
	cvSpikyAbove           = 0.50
	peakToTroughSpikyAbove = 5.0
	trendSlopeRampAbove    = 2.0
)

// Classify applies the priority-ordered pattern rules from design §7.
// First match wins. The order matters: a workload that's both periodic
// AND high-cv must classify as periodic (the cycle dominates the noise).
func Classify(f Features) string {
	switch {
	case f.CV < cvFlatBelow:
		return PatternFlat
	case f.TodCorrelation > tdCorrelationAbove:
		return PatternPeriodic
	case f.CV > cvSpikyAbove && f.PeakToTrough > peakToTroughSpikyAbove:
		return PatternSpiky
	case math.Abs(f.TrendSlope) > trendSlopeRampAbove:
		return PatternGradualRamp
	default:
		return PatternDefault
	}
}
