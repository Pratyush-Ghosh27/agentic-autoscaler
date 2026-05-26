/*
Copyright 2026.
*/

package classifier_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/classifier"
)

// testdataPath resolves <repo-root>/testdata/<name> based on this test
// file's location, so tests work regardless of the working directory.
func testdataPath(name string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", name)
}

type dataPoint struct {
	RPS float64 `json:"rps"`
}

func loadSeries(t *testing.T, name string) []float64 {
	t.Helper()
	data, err := os.ReadFile(testdataPath(name))
	require.NoError(t, err, "fixture %s not found — run `go run ./hack/synthetic --output=testdata --seed=42`", name)
	var pts []dataPoint
	require.NoError(t, json.Unmarshal(data, &pts))
	out := make([]float64, len(pts))
	for i, p := range pts {
		out[i] = p.RPS
	}
	return out
}

func absF(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// -----------------------------------------------------------------------
// HourlyAutocorrLag — F4a + G11
// -----------------------------------------------------------------------

// TestHourlyAutocorrLag pins F4a: the hourly-autocorrelation lag is
// derived from the cold-path resolution as 60 / resolutionMin so the
// "1 hour ago vs now" comparison stays correct at any cadence. At the
// v2 default resolution=5 the lag is 12 (12 × 5-min steps = 60 min);
// at the legacy resolution=1 the lag remains 60 (matching v1 TodLag).
func TestHourlyAutocorrLag(t *testing.T) {
	cases := []struct {
		resolutionMin int
		wantLag       int
	}{
		{1, 60},
		{2, 30},
		{5, 12},
		{10, 6},
		{12, 5},
	}
	for _, tc := range cases {
		got := classifier.HourlyAutocorrLag(tc.resolutionMin)
		assert.Equal(t, tc.wantLag, got,
			"HourlyAutocorrLag(%d) = %d, want %d", tc.resolutionMin, got, tc.wantLag)
	}
}

// TestHourlyAutocorrLag_DefendsZeroResolution: a malformed caller must
// not divide-by-zero. We return 0 (effectively disabling the feature)
// rather than panicking; config.validate() catches the bad config.
func TestHourlyAutocorrLag_DefendsZeroResolution(t *testing.T) {
	assert.Equal(t, 0, classifier.HourlyAutocorrLag(0))
	assert.Equal(t, 0, classifier.HourlyAutocorrLag(-1))
}

// TestHourlyAutocorrLag_AgreesWithLegacyTodLag pins that we don't drift
// from the v1 contract at the v1 cadence: HourlyAutocorrLag(1) must
// equal the existing TodLag constant.
func TestHourlyAutocorrLag_AgreesWithLegacyTodLag(t *testing.T) {
	assert.Equal(t, classifier.TodLag, classifier.HourlyAutocorrLag(1))
}

// -----------------------------------------------------------------------
// ExtractFeatures
// -----------------------------------------------------------------------

func TestExtractFeatures_FlatSeries(t *testing.T) {
	f := classifier.ExtractFeatures(loadSeries(t, "flat_1440.json"))
	assert.Less(t, f.CV, 0.10, "flat fixture cv should clear the flat threshold")
	assert.Greater(t, f.PeakToTrough, 0.0)
}

func TestExtractFeatures_PeriodicSeries(t *testing.T) {
	f := classifier.ExtractFeatures(loadSeries(t, "periodic_1440.json"))
	assert.Greater(t, f.TodCorrelation, 0.70)
}

func TestExtractFeatures_SpikySeries(t *testing.T) {
	f := classifier.ExtractFeatures(loadSeries(t, "spiky_1440.json"))
	assert.Greater(t, f.CV, 0.50)
	assert.Greater(t, f.PeakToTrough, 5.0)
}

func TestExtractFeatures_RampSeries(t *testing.T) {
	f := classifier.ExtractFeatures(loadSeries(t, "gradual_ramp_1440.json"))
	assert.Greater(t, absF(f.TrendSlope), 2.0)
}

func TestExtractFeatures_EmptySeries(t *testing.T) {
	f := classifier.ExtractFeatures(nil)
	assert.Equal(t, 0.0, f.CV)
	assert.Equal(t, 0.0, f.TodCorrelation)
	assert.Equal(t, 0.0, f.PeakToTrough)
	assert.Equal(t, 0.0, f.TrendSlope)
}

func TestExtractFeatures_LowMeanReturnsZeroCV(t *testing.T) {
	// Series with mean < 1 must produce CV == 0 to avoid div-by-near-zero.
	series := []float64{0.1, 0.2, 0.05}
	f := classifier.ExtractFeatures(series)
	assert.Equal(t, 0.0, f.CV)
}

// TestExtractFeatures_PeakToTroughUsesMaxMeanGuard pins F28: the
// peakToTrough denominator is max(mean, CVGuardMeanRPS), not mean+1.
// For low-mean series this prevents a small absolute spike from
// producing a misleadingly large peak-to-trough ratio.
//
//	mean = 0.65, p99 = 2.0
//	old denom (m + 1)              → 2.0 / 1.65 ≈ 1.21
//	new denom max(m, CVGuardMeanRPS) → 2.0 / 1.0  = 2.0
func TestExtractFeatures_PeakToTroughUsesMaxMeanGuard(t *testing.T) {
	series := []float64{0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 2.0}
	f := classifier.ExtractFeatures(series)
	assert.InDelta(t, 2.0, f.PeakToTrough, 0.05,
		"expected p99=2.0 / max(mean=0.65, 1.0) = 2.0 (F28)")
}

// TestCVGuardMeanRPSConstantExists pins F29: the CV-zero-guard
// threshold is a named, package-level variable so it can be tuned
// per-deployment via config (T13 wires the config side).
func TestCVGuardMeanRPSConstantExists(t *testing.T) {
	assert.InDelta(t, 1.0, classifier.CVGuardMeanRPS, 0.0001,
		"CVGuardMeanRPS default must be 1.0 rps")
}

// TestExtractFeatures_PeakToTroughHighMeanUnchanged pins that on a
// realistic high-mean series the new denominator behaves like the
// old one to within rounding (because max(m, 1) == m when m ≥ 1):
// p99 / m vs p99 / (m + 1) is essentially the same for m ≫ 1.
func TestExtractFeatures_PeakToTroughHighMeanUnchanged(t *testing.T) {
	series := make([]float64, 100)
	for i := range series {
		series[i] = 100 // mean ≈ 100, max ≈ 100, p99 ≈ 100
	}
	series[99] = 500 // one spike
	f := classifier.ExtractFeatures(series)
	// p99 of len=100 sorted with one 500: idx = ceil(0.99*100)-1 = 98 → still 100.
	// peakToTrough = 100 / max(mean, 1) ≈ 100/104 ≈ 0.96
	assert.InDelta(t, 1.0, f.PeakToTrough, 0.05)
}

func TestExtractFeatures_BelowTodOverlapReturnsZero(t *testing.T) {
	// Need 60+10=70 points for tod_correlation; 50 points → 0.
	series := make([]float64, 50)
	for i := range series {
		series[i] = 100 + float64(i)
	}
	f := classifier.ExtractFeatures(series)
	assert.Equal(t, 0.0, f.TodCorrelation)
}

func TestExtractFeatures_TrendSlopeOnFlatSeries(t *testing.T) {
	series := make([]float64, 100)
	for i := range series {
		series[i] = 200
	}
	f := classifier.ExtractFeatures(series)
	assert.True(t, math.Abs(f.TrendSlope) < 0.001, "flat series should have ~0 slope, got %v", f.TrendSlope)
}

func TestExtractFeatures_TrendSlopeKnownLinear(t *testing.T) {
	// y = 10 + 2*i → slope must be ~2.0
	series := make([]float64, 50)
	for i := range series {
		series[i] = 10 + 2*float64(i)
	}
	f := classifier.ExtractFeatures(series)
	assert.InDelta(t, 2.0, f.TrendSlope, 0.001)
}
