/*
Copyright 2026.
*/

package classifier

import (
	"errors"
	"math"
)

// ErrInsufficientPoints signals that the input series has fewer than
// minThreshold samples and therefore cannot be classified. Callers
// should emit a pattern_unknown event and leave classifiedParams alone.
var ErrInsufficientPoints = errors.New("classifier: insufficient history points")

// PipelineResult is the full output of a v1 classification run.
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

// PipelineConfig groups the resolution-dependent knobs that the v2
// cold-path pipeline needs to compute features and the context block.
// The worker reads these from config.Config and threads them through.
// G10 + G11.
type PipelineConfig struct {
	// ResolutionMin is the cold-path PromQL step (in minutes). At v2
	// default this is 5; legacy v1 callers pass 1.
	ResolutionMin int
	// HourlyProfileMinHours is the minimum number of distinct UTC
	// hours that must be populated for HourlyProfileValid to flip
	// true. Default 12.
	HourlyProfileMinHours int
	// CVGuardMeanRPS is the mean threshold below which CV is forced
	// to 0 and which clamps the peakToTrough denominator (F28, F29).
	CVGuardMeanRPS float64
	// StartHourUTC is the wall-clock hour (0..23) at which series[0]
	// was sampled. The worker computes this as start.UTC().Hour() and
	// passes it through; tests can pass 0 to anchor the profile to
	// midnight UTC.
	StartHourUTC int
}

// ContextOutput is the cold-path-computed scalar features and 24-bin
// hourly profile that the worker writes to status.classifiedParams.context.
// Fields mirror v1alpha1.ContextFields. G10.
type ContextOutput struct {
	BaselineRPS        int32
	PeakP95RPS         int32
	Trend24hSlope      float64
	HourlyProfile      []int32
	HourlyProfileValid bool
}

// PipelineResultV2 embeds PipelineResult and adds the context block.
// Existing v1 consumers can continue to read .PipelineResult; v2
// consumers (worker.patchStatus) read .Context.
type PipelineResultV2 struct {
	PipelineResult
	Context *ContextOutput
}

// RunPipelineV2 runs the v2 cold-path pipeline. It threads
// resolutionMin and CVGuardMeanRPS through feature extraction so
// CV/peakToTrough/trendSlope and the hourly autocorrelation are all
// resolution-aware, then computes the context block (baseline_rps,
// peak_p95_rps, trend_24h_slope, hourly_profile, hourly_profile_valid).
//
// Callers should use this in the cold-path worker; existing tests
// that pin the v1 contract may continue to call RunPipeline.
//
// G10 + G11.
func RunPipelineV2(
	series []float64,
	highConfThreshold, minThreshold int,
	minReplicas, maxReplicas int32,
	cfg PipelineConfig,
) (PipelineResultV2, error) {
	if len(series) < minThreshold {
		return PipelineResultV2{}, ErrInsufficientPoints
	}

	resolution := cfg.ResolutionMin
	if resolution < 1 {
		resolution = 1
	}
	guard := cfg.CVGuardMeanRPS
	if guard <= 0 {
		guard = CVGuardMeanRPS
	}

	// Resolution-aware feature extraction.
	m := mean(series)
	sd := stddev(series, m)

	var cv float64
	if m >= guard {
		cv = sd / m
	}
	denom := math.Max(m, guard)
	p99 := percentile(series, 0.99)
	peakToTrough := p99 / denom

	slopeRpsPerMin := TrendSlopeRpsPerMin(series, resolution)
	lag := HourlyAutocorrLag(resolution)
	// Use the raw rps/sample slope to detrend — that is what the
	// autocorrelation expects.
	rawSlope := trendSlope(series)
	todCorr := todCorrelation(detrend(series, rawSlope), lag, MinTodOverlap)

	f := Features{
		CV:             cv,
		PeakToTrough:   peakToTrough,
		TodCorrelation: todCorr,
		TrendSlope:     slopeRpsPerMin,
	}
	pattern := ClassifyWithMean(f, m)
	conf := Confidence(len(series), highConfThreshold, minThreshold)
	params := ComputeParams(f, minReplicas, maxReplicas)

	profile, valid := ComputeHourlyProfile(series, resolution, cfg.StartHourUTC, cfg.HourlyProfileMinHours)
	ctx := &ContextOutput{
		BaselineRPS:        ComputeBaselineRPS(series),
		PeakP95RPS:         ComputePeakP95RPS(series),
		Trend24hSlope:      slopeRpsPerMin,
		HourlyProfile:      profile,
		HourlyProfileValid: valid,
	}

	return PipelineResultV2{
		PipelineResult: PipelineResult{
			Pattern:       pattern,
			Confidence:    conf,
			Params:        params,
			HistoryPoints: len(series),
			Features:      f,
		},
		Context: ctx,
	}, nil
}
