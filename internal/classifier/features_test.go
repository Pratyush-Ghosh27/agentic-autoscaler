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
