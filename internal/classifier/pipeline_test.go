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
