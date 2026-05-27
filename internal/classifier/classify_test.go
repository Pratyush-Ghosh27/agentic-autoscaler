/*
Copyright 2026.
*/

package classifier_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/classifier"
)

// -----------------------------------------------------------------------
// Priority-ordered classification — first match wins.
// -----------------------------------------------------------------------

// TestClassify_PriorityOrder pins the v2 priority-ordered pattern rules.
//
// Note: Classify(f) is implemented as ClassifyWithMean(f, 1.0). The
// gradual_ramp branch is therefore very sensitive (any |slope| above
// 0.20/1440 ≈ 1.4e-4 fires it). Production code calls
// ClassifyWithMean(f, realMean) which gives a calibrated threshold;
// this test pins the single-arg helper's semantics. F26 + G11.
func TestClassify_PriorityOrder(t *testing.T) {
	cases := []struct {
		name string
		f    classifier.Features
		want string
	}{
		{
			"flat wins at low cv even with high slope",
			classifier.Features{CV: 0.05, HourlyAutocorr: 0.8, PeakToTrough: 6, TrendSlope: 3},
			classifier.PatternFlat,
		},
		{
			"periodic wins over spiky regardless of slope",
			classifier.Features{CV: 0.60, HourlyAutocorr: 0.75, PeakToTrough: 6, TrendSlope: 0},
			classifier.PatternPeriodic,
		},
		{
			"spiky needs cv>0.50 AND pt>5 (zero slope so ramp does not pre-empt)",
			classifier.Features{CV: 0.60, HourlyAutocorr: 0.3, PeakToTrough: 6, TrendSlope: 0},
			classifier.PatternSpiky,
		},
		{
			"gradual_ramp on positive slope",
			classifier.Features{CV: 0.20, HourlyAutocorr: 0.3, PeakToTrough: 2, TrendSlope: 3.0},
			classifier.PatternGradualRamp,
		},
		{
			"gradual_ramp on negative slope",
			classifier.Features{CV: 0.20, HourlyAutocorr: 0.3, PeakToTrough: 2, TrendSlope: -2.5},
			classifier.PatternGradualRamp,
		},
		{
			// At mean=1.0 the daily-drift fraction is |slope|*1440;
			// to stay in `default` the slope must be below ≈1.4e-4.
			"default fallthrough requires near-zero slope at mean=1",
			classifier.Features{CV: 0.20, HourlyAutocorr: 0.3, PeakToTrough: 2, TrendSlope: 1e-5},
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
			classifier.Features{CV: 0.15, HourlyAutocorr: 0.70},
			classifier.PatternDefault,
		},
		{
			"tod just above 0.70 IS periodic",
			classifier.Features{CV: 0.15, HourlyAutocorr: 0.701},
			classifier.PatternPeriodic,
		},
		{
			"spiky needs both cv>0.50 AND pt>5",
			classifier.Features{CV: 0.51, PeakToTrough: 4.9, HourlyAutocorr: 0.1},
			classifier.PatternDefault,
		},
		{
			// v2 (F26): with mean=1, the threshold is |slope|*1440 > 0.20,
			// so even tiny slopes fire the ramp rule. Below the boundary
			// (|slope| ≤ 0.20/1440 ≈ 1.388e-4) we stay in `default`.
			"slope just at the relative-threshold boundary stays default",
			classifier.Features{CV: 0.15, HourlyAutocorr: 0.1, PeakToTrough: 2, TrendSlope: 1.388e-4},
			classifier.PatternDefault,
		},
		{
			"slope just above the relative-threshold boundary IS ramp",
			classifier.Features{CV: 0.15, HourlyAutocorr: 0.1, PeakToTrough: 2, TrendSlope: 1.4e-4},
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
			// Use the v2 entry point with the real series mean so the
			// gradual_ramp threshold is calibrated to the workload.
			seriesMean := mean(series)
			assert.Equal(t, tc.want, classifier.ClassifyWithMean(f, seriesMean),
				"features=%+v mean=%v", f, seriesMean)
		})
	}
}

// mean is a tiny helper kept inside the test package to avoid pulling
// any dependency into the production package surface.
func mean(s []float64) float64 {
	if len(s) == 0 {
		return 0
	}
	var sum float64
	for _, v := range s {
		sum += v
	}
	return sum / float64(len(s))
}

// -----------------------------------------------------------------------
// ClassifyWithMean — v2 relative-threshold gradual_ramp rule (F26)
// -----------------------------------------------------------------------

// TestClassifyWithMean_GradualRampRelativeThreshold pins F26: a slope
// that drifts more than GradualRampDailyDriftFrac of the workload's
// mean over 24 hours is `gradual_ramp`. At mean=100 with slope=0.015
// the daily drift is 0.015*1440/100 = 0.216 > 0.20 → ramp.
func TestClassifyWithMean_GradualRampRelativeThreshold(t *testing.T) {
	f := classifier.Features{CV: 0.30, PeakToTrough: 2.0, HourlyAutocorr: 0.20, TrendSlope: 0.015}
	got := classifier.ClassifyWithMean(f, 100.0)
	assert.Equal(t, classifier.PatternGradualRamp, got,
		"daily drift = 0.015*1440/100 = 0.216 > 0.20 should fire ramp")

	// Sanity: the same slope is well below the legacy absolute-2.0
	// threshold, so this is a real behavioural change (F26).
	if math.Abs(f.TrendSlope) > 2.0 {
		t.Fatal("test setup error: slope should be below the legacy absolute threshold")
	}
}

// TestClassifyWithMean_HighMeanWidensTolerance pins the symmetric
// case: at high mean, a slope that *was* a ramp under the absolute
// rule is no longer a ramp because the relative drift is small.
//
//	mean=10000, slope=0.5 → 0.5*1440/10000 = 0.072 < 0.20 → default.
func TestClassifyWithMean_HighMeanWidensTolerance(t *testing.T) {
	f := classifier.Features{CV: 0.20, PeakToTrough: 2.0, HourlyAutocorr: 0.20, TrendSlope: 0.5}
	got := classifier.ClassifyWithMean(f, 10000.0)
	assert.Equal(t, classifier.PatternDefault, got,
		"daily drift 0.072 must NOT fire ramp at mean=10k under v2 rule")
}

// TestClassifyWithMean_NegativeSlopeAlsoFires pins symmetry: the
// relative threshold uses |slope| so falling traffic also classifies
// as ramp when the magnitude exceeds the threshold.
func TestClassifyWithMean_NegativeSlopeAlsoFires(t *testing.T) {
	f := classifier.Features{CV: 0.30, PeakToTrough: 2.0, HourlyAutocorr: 0.20, TrendSlope: -0.020}
	got := classifier.ClassifyWithMean(f, 100.0)
	assert.Equal(t, classifier.PatternGradualRamp, got)
}

// TestGradualRampDailyDriftFracExposed pins the threshold constant so
// runbooks / operators can reference it by name.
func TestGradualRampDailyDriftFracExposed(t *testing.T) {
	assert.InDelta(t, 0.20, classifier.GradualRampDailyDriftFrac, 0.0001)
}
