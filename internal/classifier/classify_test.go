/*
Copyright 2026.
*/

package classifier_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/classifier"
)

// -----------------------------------------------------------------------
// Priority-ordered classification — first match wins.
// -----------------------------------------------------------------------

func TestClassify_PriorityOrder(t *testing.T) {
	cases := []struct {
		name string
		f    classifier.Features
		want string
	}{
		{
			"flat wins at low cv",
			classifier.Features{CV: 0.05, TodCorrelation: 0.8, PeakToTrough: 6, TrendSlope: 3},
			classifier.PatternFlat,
		},
		{
			"periodic wins over spiky",
			classifier.Features{CV: 0.60, TodCorrelation: 0.75, PeakToTrough: 6, TrendSlope: 0},
			classifier.PatternPeriodic,
		},
		{
			"spiky needs cv>0.50 AND pt>5",
			classifier.Features{CV: 0.60, TodCorrelation: 0.3, PeakToTrough: 6, TrendSlope: 0},
			classifier.PatternSpiky,
		},
		{
			"gradual_ramp on positive slope",
			classifier.Features{CV: 0.20, TodCorrelation: 0.3, PeakToTrough: 2, TrendSlope: 3.0},
			classifier.PatternGradualRamp,
		},
		{
			"gradual_ramp on negative slope",
			classifier.Features{CV: 0.20, TodCorrelation: 0.3, PeakToTrough: 2, TrendSlope: -2.5},
			classifier.PatternGradualRamp,
		},
		{
			"default fallthrough",
			classifier.Features{CV: 0.20, TodCorrelation: 0.3, PeakToTrough: 2, TrendSlope: 1.0},
			classifier.PatternDefault,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, classifier.Classify(tc.f))
		})
	}
}

// -----------------------------------------------------------------------
// Boundary tests — exact-threshold behaviour pinned.
// -----------------------------------------------------------------------

func TestClassify_Boundaries(t *testing.T) {
	cases := []struct {
		name string
		f    classifier.Features
		want string
	}{
		{
			"cv exactly 0.10 is NOT flat",
			classifier.Features{CV: 0.10},
			classifier.PatternDefault,
		},
		{
			"cv just below 0.10 IS flat",
			classifier.Features{CV: 0.099},
			classifier.PatternFlat,
		},
		{
			"tod exactly 0.70 is NOT periodic",
			classifier.Features{CV: 0.15, TodCorrelation: 0.70},
			classifier.PatternDefault,
		},
		{
			"tod just above 0.70 IS periodic",
			classifier.Features{CV: 0.15, TodCorrelation: 0.701},
			classifier.PatternPeriodic,
		},
		{
			"spiky needs both cv>0.50 AND pt>5",
			classifier.Features{CV: 0.51, PeakToTrough: 4.9, TodCorrelation: 0.1},
			classifier.PatternDefault,
		},
		{
			"slope exactly 2.0 is NOT ramp",
			classifier.Features{CV: 0.15, TodCorrelation: 0.1, PeakToTrough: 2, TrendSlope: 2.0},
			classifier.PatternDefault,
		},
		{
			"slope just above 2.0 IS ramp",
			classifier.Features{CV: 0.15, TodCorrelation: 0.1, PeakToTrough: 2, TrendSlope: 2.01},
			classifier.PatternGradualRamp,
		},
		{
			"spiky boundary cv just above 0.50 with high pt",
			classifier.Features{CV: 0.501, PeakToTrough: 5.01},
			classifier.PatternSpiky,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, classifier.Classify(tc.f))
		})
	}
}

// -----------------------------------------------------------------------
// End-to-end self-consistency: each generated fixture classifies to its
// intended pattern. Pins the synthetic-data → classifier contract.
// -----------------------------------------------------------------------

func TestClassify_OnSyntheticFixtures(t *testing.T) {
	cases := []struct {
		fixture string
		want    string
	}{
		{"flat_1440.json", classifier.PatternFlat},
		{"periodic_1440.json", classifier.PatternPeriodic},
		{"spiky_1440.json", classifier.PatternSpiky},
		{"gradual_ramp_1440.json", classifier.PatternGradualRamp},
		{"default_1440.json", classifier.PatternDefault},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			series := loadSeries(t, tc.fixture)
			f := classifier.ExtractFeatures(series)
			assert.Equal(t, tc.want, classifier.Classify(f),
				"features=%+v", f)
		})
	}
}
