/*
Copyright 2026.
*/

package main

import (
	"math"
	"math/rand"
)

// GenFlat produces a nearly-constant series with tiny Gaussian noise.
// Target: cv < 0.10 (mean=200, stddev≈5 → cv≈0.025).
func GenFlat(seed int64, n int) []float64 {
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec
	const base = 200.0
	out := make([]float64, n)
	for i := range out {
		out[i] = math.Max(0, base+rng.NormFloat64()*5)
	}
	return out
}

// GenPeriodic produces a series with a strong 60-minute repeating cycle.
// Target: hourly_autocorr > 0.70 (named tod_correlation in v1; the Go
// field is Features.HourlyAutocorr — see internal/classifier/features.go).
//
// The amplitude (100) is chosen large relative to the noise (stddev=20)
// so the lag-N Pearson correlation stays well above the 0.70 threshold
// even after the smaller noise term is applied. N is the cadence-aware
// lag: 60 at 1-min (v1) / 12 at 5-min (v2 default cold path).
func GenPeriodic(seed int64, n int) []float64 {
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec
	out := make([]float64, n)
	for i := range out {
		cycle := 100.0 * math.Sin(2*math.Pi*float64(i)/60.0)
		out[i] = math.Max(0, 200+cycle+rng.NormFloat64()*20)
	}
	return out
}

// GenSpiky produces a series with random high-magnitude bursts on top of
// a low base. Target: cv > 0.50 AND peak_to_trough > 5.
//
// 5% spike probability with magnitude in [500, 1000] over a base of ~50
// pushes the p99/(mean+1) ratio comfortably above 5.
func GenSpiky(seed int64, n int) []float64 {
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec
	out := make([]float64, n)
	for i := range out {
		base := 50.0 + rng.NormFloat64()*10
		if rng.Float64() < 0.05 {
			base += 500 + rng.Float64()*500
		}
		out[i] = math.Max(0, base)
	}
	return out
}

// GenRamp produces a steadily increasing series. Target: |trend_slope| > 2.0
// rps/min. With slope=3 rps/min the linear-regression fit comfortably
// exceeds the threshold.
func GenRamp(seed int64, n int) []float64 {
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec
	out := make([]float64, n)
	for i := range out {
		out[i] = math.Max(0, 100+3.0*float64(i)+rng.NormFloat64()*20)
	}
	return out
}

// GenDefault produces moderate variance without triggering any specific
// rule. Target: cv ∈ [0.10, 0.50], hourly_autocorr < 0.70, peak_to_trough
// ≤ 5, |slope| < 2.
func GenDefault(seed int64, n int) []float64 {
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec
	out := make([]float64, n)
	for i := range out {
		out[i] = math.Max(0, 200+rng.NormFloat64()*50)
	}
	return out
}
