/*
Copyright 2026.
*/

package classifier_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/classifier"
)

func TestRunPipeline_OnPeriodicFixture(t *testing.T) {
	series := loadSeries(t, "periodic_1440.json")
	result, err := classifier.RunPipeline(series, 240, 70, 2, 10)
	require.NoError(t, err)
	assert.Equal(t, classifier.PatternPeriodic, result.Pattern)
	assert.Equal(t, classifier.ConfidenceHigh, result.Confidence)
	assert.Equal(t, classifier.ForecasterProphet, result.Params.PreferredForecaster)
	assert.Equal(t, 1440, result.HistoryPoints)
}

func TestRunPipeline_OnSpikyFixture(t *testing.T) {
	series := loadSeries(t, "spiky_1440.json")
	result, err := classifier.RunPipeline(series, 240, 70, 2, 10)
	require.NoError(t, err)
	assert.Equal(t, classifier.PatternSpiky, result.Pattern)
	assert.Equal(t, classifier.ConfidenceHigh, result.Confidence)
	assert.Greater(t, result.Params.MaxStep, int32(1))
	// T15 (G19): spiky pattern must route to gbdt_quantile end-to-end
	// through RunPipeline + ComputeParams. Pins the symmetric Go-side
	// counterpart of the Python F22 invariant.
	assert.Equal(t, classifier.ForecasterGBDTQuantile, result.Params.PreferredForecaster,
		"spiky pattern must select gbdt_quantile via the pattern-driven selector (G19)")
}

func TestRunPipeline_InsufficientPoints(t *testing.T) {
	series := loadSeries(t, "insufficient_50.json")
	_, err := classifier.RunPipeline(series, 240, 70, 2, 10)
	require.Error(t, err)
	assert.ErrorIs(t, err, classifier.ErrInsufficientPoints)
}

func TestRunPipeline_EmptySeriesIsInsufficient(t *testing.T) {
	_, err := classifier.RunPipeline(nil, 240, 70, 2, 10)
	assert.ErrorIs(t, err, classifier.ErrInsufficientPoints)
}

func TestRunPipeline_JustBelowMinThreshold(t *testing.T) {
	series := make([]float64, 69)
	_, err := classifier.RunPipeline(series, 240, 70, 2, 10)
	assert.ErrorIs(t, err, classifier.ErrInsufficientPoints)
}

func TestRunPipeline_ExactlyMinPointsGivesMediumConfidence(t *testing.T) {
	series := make([]float64, 70)
	for i := range series {
		series[i] = 100
	}
	result, err := classifier.RunPipeline(series, 240, 70, 2, 10)
	require.NoError(t, err)
	assert.Equal(t, classifier.ConfidenceMedium, result.Confidence)
}

func TestRunPipeline_FlatBoundaryFixture(t *testing.T) {
	// 70-point flat fixture should classify as flat with medium confidence.
	series := loadSeries(t, "flat_70.json")
	result, err := classifier.RunPipeline(series, 240, 70, 2, 10)
	require.NoError(t, err)
	assert.Equal(t, classifier.PatternFlat, result.Pattern)
	assert.Equal(t, classifier.ConfidenceMedium, result.Confidence)
}

// TestRunPipelineV2_SpikyPatternSelectsGBDTQuantile pins the G19
// invariant end-to-end on the v2 cold-path pipeline: a series the
// classifier identifies as `spiky` MUST produce PreferredForecaster
// = gbdt_quantile on the persisted ClassifiedOutput. This is the
// integration counterpart of TestComputeParams_PatternDrivenForecaster
// — it exercises the full path classify -> pattern -> ComputeParams
// rather than just unit-testing the selector in isolation.
func TestRunPipelineV2_SpikyPatternSelectsGBDTQuantile(t *testing.T) {
	// Synthetic spiky series at 5-min cadence over 24h: a low
	// baseline of 20 rps with periodic 200-rps bursts every 30
	// buckets (= 2.5h). Peak-to-trough of 10x and CV > 1.0 reliably
	// fire the spiky thresholds.
	series := make([]float64, 288)
	for i := range series {
		if i%30 == 0 {
			series[i] = 200.0
		} else {
			series[i] = 20.0
		}
	}

	cfg := classifier.PipelineConfig{
		ResolutionMin:         5,
		HourlyProfileMinHours: 12,
		CVGuardMeanRPS:        1.0,
		StartHourUTC:          0,
	}
	result, err := classifier.RunPipelineV2(series, 240, 72, 1, 10, cfg)
	require.NoError(t, err)
	require.Equal(t, classifier.PatternSpiky, result.Pattern,
		"synthetic burst series must classify as spiky (got %q) — "+
			"if this fails, the series needs re-tuning, not the selector",
		result.Pattern)
	assert.Equal(t,
		classifier.ForecasterGBDTQuantile,
		result.Params.PreferredForecaster,
		"spiky pattern must route to gbdt_quantile via the v2 pipeline (G19)",
	)
}

// -----------------------------------------------------------------------
// RunPipelineV2 — context-bearing pipeline (G10 + G11)
// -----------------------------------------------------------------------

// TestRunPipelineV2_ReturnsContext pins G10/G11: the v2 pipeline
// produces a populated Context block alongside the legacy
// PipelineResult, ready for the worker to write to status.
func TestRunPipelineV2_ReturnsContext(t *testing.T) {
	// 100 points of ~50 RPS with a small periodic wobble.
	series := make([]float64, 100)
	for i := range series {
		series[i] = float64(50 + i%10)
	}
	cfg := classifier.PipelineConfig{
		ResolutionMin:         5,
		HourlyProfileMinHours: 12,
		CVGuardMeanRPS:        1.0,
		StartHourUTC:          0,
	}
	result, err := classifier.RunPipelineV2(series, 240, 72, 1, 10, cfg)
	require.NoError(t, err)
	require.NotNil(t, result.Context)
	assert.NotZero(t, result.Context.BaselineRPS, "non-trivial series ⇒ non-zero baseline")
	assert.NotZero(t, result.Context.PeakP95RPS, "non-trivial series ⇒ non-zero peak p95")
	assert.Len(t, result.Context.HourlyProfile, 24)
	// 100 points at 5-min cadence ≈ 8h20m, well below the 12h gate.
	assert.False(t, result.Context.HourlyProfileValid,
		"100 points ≈ 8h coverage must NOT pass the 12-hour gate")
}

// TestRunPipelineV2_PreservesLegacyResult: the embedded
// PipelineResult is populated identically to RunPipeline at the v1
// cadence (resolution=1), so existing pipeline semantics are pinned.
func TestRunPipelineV2_PreservesLegacyResult(t *testing.T) {
	series := loadSeries(t, "periodic_1440.json")
	cfg := classifier.PipelineConfig{
		ResolutionMin:         1,
		HourlyProfileMinHours: 12,
		CVGuardMeanRPS:        1.0,
		StartHourUTC:          0,
	}
	v2, err := classifier.RunPipelineV2(series, 240, 70, 2, 10, cfg)
	require.NoError(t, err)
	v1, err := classifier.RunPipeline(series, 240, 70, 2, 10)
	require.NoError(t, err)
	assert.Equal(t, v1.Pattern, v2.Pattern)
	assert.Equal(t, v1.Confidence, v2.Confidence)
	assert.Equal(t, v1.HistoryPoints, v2.HistoryPoints)
}

// TestRunPipelineV2_InsufficientPoints: same gate as v1, returns
// ErrInsufficientPoints with no context.
func TestRunPipelineV2_InsufficientPoints(t *testing.T) {
	series := make([]float64, 21) // below v2 default floor of 22
	cfg := classifier.PipelineConfig{
		ResolutionMin:         5,
		HourlyProfileMinHours: 12,
		CVGuardMeanRPS:        1.0,
	}
	_, err := classifier.RunPipelineV2(series, 240, 22, 1, 10, cfg)
	assert.ErrorIs(t, err, classifier.ErrInsufficientPoints)
}

// TestRunPipelineV2_24hCoverageIsValid: 288 points at 5-min cadence
// covers exactly 24h and the hourly profile is marked valid.
func TestRunPipelineV2_24hCoverageIsValid(t *testing.T) {
	series := make([]float64, 288)
	for i := range series {
		series[i] = float64(50 + (i/12)%24)
	}
	cfg := classifier.PipelineConfig{
		ResolutionMin:         5,
		HourlyProfileMinHours: 12,
		CVGuardMeanRPS:        1.0,
		StartHourUTC:          0,
	}
	result, err := classifier.RunPipelineV2(series, 240, 72, 1, 10, cfg)
	require.NoError(t, err)
	require.NotNil(t, result.Context)
	assert.True(t, result.Context.HourlyProfileValid,
		"24h of 5-min data must satisfy the 12-hour-min-coverage gate")
}
