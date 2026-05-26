/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package classifier implements traffic-pattern classification for the
// AgenticAutoscaler's cold-path worker. The package is split into pure
// computation (this file, classify.go, confidence.go, params.go,
// pipeline.go) and the goroutine that wraps it in I/O (worker.go).
//
// All formulae and thresholds come from docs/design.md §7. Renaming a
// constant or shifting a threshold is a behavioural change and must be
// reflected in the spec.
package classifier

import (
	"math"
	"sort"
)

// TodLag is the legacy 1-min-cadence hourly-autocorr lag. It is kept
// only for v1 callers; new code should use HourlyAutocorrLag(resolutionMin).
// At the v2 default cold-path resolution of 5 minutes the correct lag
// is 12 (= 60/5), not 60.
const TodLag = 60

// MinTodOverlap is the minimum number of overlapping samples required
// for a meaningful Pearson correlation. With lag = 60/resolutionMin
// and Overlap=10, we need at least lag+10 points before
// hourly_autocorr can be computed. At resolution=5 that's 22 points;
// at resolution=1 that's 70 (matching v1).
const MinTodOverlap = 10

// HourlyAutocorrLag returns the lag (in samples) corresponding to one
// hour at the given downsample resolution. For the v2 cold path at
// 5-min cadence the lag is 12; for the legacy 1-min cadence the lag
// is 60 (matching the deprecated TodLag constant).
//
// Returns 0 for non-positive resolutions to avoid division-by-zero;
// config.validate() catches a misconfigured ContextResolutionMinutes
// before a real classifier run reaches this code path. F4a + G11.
func HourlyAutocorrLag(resolutionMin int) int {
	if resolutionMin <= 0 {
		return 0
	}
	return 60 / resolutionMin
}

// Features holds the four extracted features from design §7.
type Features struct {
	// CV is the coefficient of variation: stddev / mean. Returns 0 when
	// mean < 1 to avoid division-by-near-zero blowup on idle series.
	CV float64

	// PeakToTrough is the p99 / (mean + 1) ratio. The +1 in the
	// denominator keeps low-mean series from producing pathological
	// ratios ("idle service had a brief 5 RPS spike → 5x peak ratio?").
	PeakToTrough float64

	// TodCorrelation is the Pearson correlation between the series
	// shifted by TodLag and the unshifted series. Strong positive
	// correlation indicates a recurring 60-minute pattern.
	TodCorrelation float64

	// TrendSlope is the least-squares slope (rps per sample / per minute
	// when sampling cadence is 1 min).
	TrendSlope float64
}

// ExtractFeatures computes all four features from a 1-minute-cadence
// time series. Empty input returns the zero Features.
func ExtractFeatures(series []float64) Features {
	if len(series) == 0 {
		return Features{}
	}

	m := mean(series)
	sd := stddev(series, m)

	var cv float64
	if m >= 1 {
		cv = sd / m
	}

	p99 := percentile(series, 0.99)
	peakToTrough := p99 / (m + 1)

	slope := trendSlope(series)

	return Features{
		CV:             cv,
		PeakToTrough:   peakToTrough,
		TodCorrelation: todCorrelation(detrend(series, slope), TodLag, MinTodOverlap),
		TrendSlope:     slope,
	}
}

// detrend subtracts the linear regression line (slope*i + intercept) so
// the residuals contain only deviations from the long-run trend.
//
// Without this, a monotone ramp produces near-perfect lag-N
// autocorrelation (because adjacent samples differ by a constant), which
// would route every ramp through the periodic branch.
func detrend(series []float64, slope float64) []float64 {
	n := len(series)
	if n == 0 {
		return series
	}
	mx := float64(n-1) / 2.0
	my := mean(series)
	intercept := my - slope*mx
	out := make([]float64, n)
	for i, v := range series {
		out[i] = v - (intercept + slope*float64(i))
	}
	return out
}

// -----------------------------------------------------------------------
// Pure stat helpers — kept private so the package's public surface
// remains "Features + Classify + ComputeParams + Confidence + RunPipeline".
// -----------------------------------------------------------------------

func mean(s []float64) float64 {
	var sum float64
	for _, v := range s {
		sum += v
	}
	return sum / float64(len(s))
}

func stddev(s []float64, m float64) float64 {
	var sum float64
	for _, v := range s {
		d := v - m
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(s)))
}

func percentile(s []float64, p float64) float64 {
	if len(s) == 0 {
		return 0
	}
	sorted := make([]float64, len(s))
	copy(sorted, s)
	sort.Float64s(sorted)
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func todCorrelation(series []float64, lag, minOverlap int) float64 {
	n := len(series)
	if n < lag+minOverlap {
		return 0
	}
	x := series[:n-lag]
	y := series[lag:]
	mx := mean(x)
	my := mean(y)
	var num, sx, sy float64
	for i := range x {
		dx := x[i] - mx
		dy := y[i] - my
		num += dx * dy
		sx += dx * dx
		sy += dy * dy
	}
	if sx == 0 || sy == 0 {
		return 0
	}
	return num / math.Sqrt(sx*sy)
}

// trendSlope returns the least-squares slope of the series.
//   - x values are 0..n-1 (sample index = minutes when cadence is 1m)
//   - returns slope in rps per sample (≡ rps/min for 1m cadence)
func trendSlope(series []float64) float64 {
	n := len(series)
	if n < 2 {
		return 0
	}
	mx := float64(n-1) / 2.0
	my := mean(series)
	var num, den float64
	for i, v := range series {
		dx := float64(i) - mx
		num += dx * (v - my)
		den += dx * dx
	}
	if den == 0 {
		return 0
	}
	return num / den
}
