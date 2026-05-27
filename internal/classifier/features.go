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
// All formulae and thresholds come from docs/design_v2.md §7. Renaming a
// constant or shifting a threshold is a behavioural change and must be
// reflected in the spec. v1 callers (RunPipeline / ExtractFeatures) are
// preserved verbatim against docs/design.md §7 for backward compatibility;
// v2 callers must use RunPipelineV2 with a PipelineConfig that carries
// the cold-path resolution.
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

// CVGuardMeanRPS is the threshold below which CV is forced to zero
// (to avoid amplifying noise on idle services) and which doubles as
// the floor on the peakToTrough denominator (F28: peakToTrough =
// p99 / max(mean, CVGuardMeanRPS)). It is a package-level variable so
// the cold-path worker can override it from config.CVGuardMeanRPS at
// startup; unit tests rely on the 1.0 default. F29.
var CVGuardMeanRPS float64 = 1.0

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

// Features holds the four extracted features from design_v2.md §7.
type Features struct {
	// CV is the coefficient of variation: stddev / mean. Returns 0 when
	// mean < 1 to avoid division-by-near-zero blowup on idle series.
	CV float64

	// PeakToTrough is the p99 / (mean + 1) ratio. The +1 in the
	// denominator keeps low-mean series from producing pathological
	// ratios ("idle service had a brief 5 RPS spike → 5x peak ratio?").
	PeakToTrough float64

	// HourlyAutocorr is the Pearson correlation between the series
	// shifted by lag = 60 / cold-path-resolution-min (TodLag at 1-min,
	// HourlyAutocorrLag(5)=12 at v2's 5-min default) and the unshifted
	// series. Strong positive correlation indicates a recurring
	// 60-minute pattern. Was named TodCorrelation in v1 (when the
	// feature was tod_correlation); see design_v2.md §7 + v2_revision
	// notes F4a/F13 for the rename narrative.
	HourlyAutocorr float64

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

	// F29: zero-guard the CV computation on idle services. Below
	// CVGuardMeanRPS, `sd / m` would amplify floating-point noise into
	// a meaningless ratio; we set CV to 0 so downstream classification
	// treats the workload as flat instead of spiky.
	var cv float64
	if m >= CVGuardMeanRPS {
		cv = sd / m
	}

	// F28: clamp the peakToTrough denominator to max(mean, CVGuardMeanRPS).
	// The old `mean + 1` denominator silently shrank the ratio for any
	// workload with mean > 0 — at high mean this barely mattered, but
	// it also blurred the boundary between "low-traffic with one spike"
	// and "real spiky pattern". Using max(mean, guard) keeps small
	// services from inflating their peak-to-trough ratio.
	p99 := percentile(series, 0.99)
	peakToTrough := p99 / math.Max(m, CVGuardMeanRPS)

	slope := trendSlope(series)

	return Features{
		CV:             cv,
		PeakToTrough:   peakToTrough,
		HourlyAutocorr: hourlyAutocorr(detrend(series, slope), TodLag, MinTodOverlap),
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

// median returns the middle value of the series (mean of the two
// middle values for even-length series). Used by the cold-path
// context computations (baseline_rps, hourly_profile per-bin) where
// outlier resistance is more important than mean's smoothness.
func median(s []float64) float64 {
	if len(s) == 0 {
		return 0
	}
	sorted := make([]float64, len(s))
	copy(sorted, s)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 0 {
		return (sorted[n/2-1] + sorted[n/2]) / 2
	}
	return sorted[n/2]
}

// -----------------------------------------------------------------------
// Context-field producers — G10 + G11
// -----------------------------------------------------------------------

// ComputeBaselineRPS returns the median RPS over the cold-path
// history window, rounded to int. Used as `baseline_rps` in the
// status `context` block. Median (not mean) so a few outliers don't
// shift the baseline. G10.
func ComputeBaselineRPS(series []float64) int32 {
	return int32(math.Round(median(series)))
}

// ComputePeakP95RPS returns the 95th-percentile RPS, rounded to int.
// Used as `peak_p95_rps` in the status `context` block. p95 (not
// max) is robust to a single freak burst while still capturing
// realistic recurring peaks. G10.
func ComputePeakP95RPS(series []float64) int32 {
	return int32(math.Round(percentile(series, 0.95)))
}

// ComputeHourlyProfile returns a 24-bin median-of-RPS-per-UTC-hour
// profile and a validity flag. The series is bucketed by
// (sampleIndex / pointsPerHour + startHourUTC) mod 24, so the caller
// must supply the wall-clock hour of series[0] in startHourUTC.
//
// `valid` is true iff at least minHours distinct hours had at least
// one sample. Forecasters consume `hourly_profile` only when valid is
// true; otherwise they ignore it.
//
// Defends against resolutionMin <= 0 by treating pointsPerHour as 1
// (effectively "every sample is its own hour bucket"); the function
// never panics or div-by-zeros. config.validate() catches the bad
// config in production. G11.
func ComputeHourlyProfile(series []float64, resolutionMin, startHourUTC, minHours int) ([]int32, bool) {
	profile := make([]int32, 24)
	if len(series) == 0 {
		return profile, false
	}
	if resolutionMin < 1 {
		// Defensive: a misconfigured caller would otherwise divide by
		// zero. Treating each sample as one hour bucket degrades
		// gracefully — config.validate() catches this in production.
		resolutionMin = 60
	}
	pointsPerHour := 60 / resolutionMin
	if pointsPerHour < 1 {
		pointsPerHour = 1
	}
	if startHourUTC < 0 {
		startHourUTC = 0
	}
	buckets := make([][]float64, 24)
	for i, v := range series {
		hourOffset := i / pointsPerHour
		hour := (startHourUTC + hourOffset) % 24
		buckets[hour] = append(buckets[hour], v)
	}
	distinctHours := 0
	for h := 0; h < 24; h++ {
		if len(buckets[h]) > 0 {
			profile[h] = int32(math.Round(median(buckets[h])))
			distinctHours++
		}
	}
	return profile, distinctHours >= minHours
}

// hourlyAutocorr computes the lag-N Pearson autocorrelation of the
// (detrended) series. With lag = 60 / cold-path-resolution-min, this
// captures hour-period repetition. Returns 0 when the overlap is too
// short or when either side has zero variance. Was named todCorrelation
// in v1; see Features.HourlyAutocorr.
func hourlyAutocorr(series []float64, lag, minOverlap int) float64 {
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

// TrendSlopeRpsPerMin computes the least-squares slope and converts
// from rps/sample to rps/min by dividing by resolutionMin. The
// `gradual_ramp` daily-drift rule (F26) and the `trend_24h_slope`
// context field (G10) both expect rps/min — using TrendSlopeRpsPerMin
// at the configured cold-path resolution keeps both unit-correct
// regardless of cadence.
//
// At resolutionMin <= 0 we fall back to the raw rps/sample slope
// (config.validate() catches bad resolutions before any real cold-path
// run reaches this code). F18 + G11.
func TrendSlopeRpsPerMin(series []float64, resolutionMin int) float64 {
	raw := trendSlope(series)
	if resolutionMin <= 0 {
		return raw
	}
	return raw / float64(resolutionMin)
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
