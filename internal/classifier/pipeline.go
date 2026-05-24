/*
Copyright 2026.
*/

package classifier

import "errors"

// ErrInsufficientPoints signals that the input series has fewer than
// minThreshold samples and therefore cannot be classified. Callers
// should emit a pattern_unknown event and leave classifiedParams alone.
var ErrInsufficientPoints = errors.New("classifier: insufficient history points")

// PipelineResult is the full output of a classification run.
type PipelineResult struct {
	Pattern       string
	Confidence    string
	Params        ClassifiedOutput
	HistoryPoints int
	Features      Features
}

// RunPipeline runs the full classification pipeline (design §6.1 steps
// 2–6): point-count gate → feature extraction → priority-ordered classify
// → confidence label → parameter formulae.
//
// minReplicas / maxReplicas come from the CRD spec; they bound the
// maxStep produced by ComputeParams so a single reconcile cannot cross
// the full range.
func RunPipeline(
	series []float64,
	highConfThreshold, minThreshold int,
	minReplicas, maxReplicas int32,
) (PipelineResult, error) {
	if len(series) < minThreshold {
		return PipelineResult{}, ErrInsufficientPoints
	}
	f := ExtractFeatures(series)
	pattern := Classify(f)
	conf := Confidence(len(series), highConfThreshold, minThreshold)
	params := ComputeParams(f, minReplicas, maxReplicas)

	return PipelineResult{
		Pattern:       pattern,
		Confidence:    conf,
		Params:        params,
		HistoryPoints: len(series),
		Features:      f,
	}, nil
}
