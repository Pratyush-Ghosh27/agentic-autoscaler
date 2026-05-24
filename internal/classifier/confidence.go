/*
Copyright 2026.
*/

package classifier

// Confidence labels. The CRD enum constrains status.classifiedParams
// .confidence to exactly these two strings; an empty string signals
// "do not write classifiedParams" (caller should have skipped).
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
)

// Confidence returns "high" when historyPoints meets highThreshold,
// "medium" when it merely meets minThreshold, and "" below minThreshold.
//
// Callers that already gated on minThreshold (RunPipeline does) will
// never see the "" return; it exists as a safety net for direct callers.
func Confidence(historyPoints, highThreshold, minThreshold int) string {
	switch {
	case historyPoints >= highThreshold:
		return ConfidenceHigh
	case historyPoints >= minThreshold:
		return ConfidenceMedium
	default:
		return ""
	}
}
