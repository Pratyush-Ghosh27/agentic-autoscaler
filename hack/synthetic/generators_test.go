/*
Copyright 2026.
*/

package main

import (
	"math"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// -----------------------------------------------------------------------
// Test helpers (unexported, in-package).
// -----------------------------------------------------------------------

func mean(s []float64) float64 {
	var sum float64
	for _, v := range s {
		sum += v
	}
	return sum / float64(len(s))
}

func stddev(s []float64) float64 {
	m := mean(s)
	var sum float64
	for _, v := range s {
		d := v - m
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(s)))
}

func percentile99(s []float64) float64 {
	sorted := make([]float64, len(s))
	copy(sorted, s)
	sort.Float64s(sorted)
	idx := int(math.Ceil(0.99*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// pearsonLag60 mirrors the classifier's tod_correlation: Pearson on
// (series[:n-60], series[60:]).
func pearsonLag60(s []float64) float64 {
	const lag = 60
	n := len(s)
	if n < lag+10 {
		return 0
	}
	x := s[:n-lag]
	y := s[lag:]
	mx, my := mean(x), mean(y)
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

func lsqSlope(s []float64) float64 {
	n := len(s)
	if n < 2 {
		return 0
	}
	mx := float64(n-1) / 2.0
	my := mean(s)
	var num, den float64
	for i, v := range s {
		dx := float64(i) - mx
		num += dx * (v - my)
		den += dx * dx
	}
	if den == 0 {
		return 0
	}
	return num / den
}

func absVal(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// -----------------------------------------------------------------------
// Generator tests.
// -----------------------------------------------------------------------

func TestGenFlat_LowCV(t *testing.T) {
	s := GenFlat(42, 1440)
	require.Len(t, s, 1440)
	cv := stddev(s) / mean(s)
	assert.Less(t, cv, 0.10, "flat series must have cv < 0.10")
}

func TestGenPeriodic_HighTodCorrelation(t *testing.T) {
	s := GenPeriodic(42, 1440)
	require.Len(t, s, 1440)
	corr := pearsonLag60(s)
	assert.Greater(t, corr, 0.70, "periodic series must have tod_correlation > 0.70")
}

func TestGenSpiky_HighCVAndPeakToTrough(t *testing.T) {
	s := GenSpiky(42, 1440)
	cv := stddev(s) / mean(s)
	pt := percentile99(s) / (mean(s) + 1)
	assert.Greater(t, cv, 0.50, "spiky cv > 0.50")
	assert.Greater(t, pt, 5.0, "spiky peak_to_trough > 5")
}

func TestGenRamp_HighTrendSlope(t *testing.T) {
	s := GenRamp(42, 1440)
	slope := lsqSlope(s)
	assert.Greater(t, absVal(slope), 2.0, "ramp must have |trend_slope| > 2.0 rps/min")
}

func TestGenDefault_FallsThrough(t *testing.T) {
	s := GenDefault(42, 1440)
	cv := stddev(s) / mean(s)
	corr := pearsonLag60(s)
	pt := percentile99(s) / (mean(s) + 1)
	slope := lsqSlope(s)
	assert.GreaterOrEqual(t, cv, 0.10, "default: not flat")
	assert.Less(t, corr, 0.70, "default: not periodic")
	assert.True(t, cv <= 0.50 || pt <= 5.0, "default: not spiky")
	assert.Less(t, absVal(slope), 2.0, "default: not ramp")
}

func TestGenerators_Deterministic(t *testing.T) {
	// Same seed must produce byte-identical output across runs — this is
	// the contract the committed testdata/ fixtures depend on.
	a := GenSpiky(42, 1440)
	b := GenSpiky(42, 1440)
	require.Len(t, a, len(b))
	for i := range a {
		assert.Equal(t, a[i], b[i], "non-deterministic at index %d", i)
	}
}
