/*
Copyright 2026.
*/

package controller

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/classifier"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/reasoning"
)

// TestMetrics_RegistrationDoesNotPanic verifies the package's init()
// MustRegister succeeded. If it had panicked we wouldn't even reach
// here, so this test mostly exists as a load-time sentinel that
// future contributors don't trip the duplicate-register check.
func TestMetrics_RegistrationDoesNotPanic(t *testing.T) {
	assert.NotNil(t, mPredictedRPS)
	assert.NotNil(t, mCurrentRPS)
	assert.NotNil(t, mRpsPerPod)
	assert.NotNil(t, mScaleEventsTotal)
	assert.NotNil(t, mClassifiedPattern)
	assert.NotNil(t, mClassifiedConfidence)
	assert.NotNil(t, mForecastFailuresTotal)
	assert.NotNil(t, mMetricsUnavailableTotal)
}

func TestObserveReconcile_SetsAllGaugesAndIncrementsCounter(t *testing.T) {
	// Reset to keep this test independent of any other test that
	// observed earlier in the same run.
	mCurrentRPS.Reset()
	mPredictedRPS.Reset()
	mRpsPerPod.Reset()

	observeReconcile("demo", "app-agentic", reasoning.ScaleUp, 123.4, 200.0, 50.0)

	assert.Equal(t, 123.4, testutil.ToFloat64(mCurrentRPS.WithLabelValues("demo", "app-agentic")))
	assert.Equal(t, 200.0, testutil.ToFloat64(mPredictedRPS.WithLabelValues("demo", "app-agentic")))
	assert.Equal(t, 50.0, testutil.ToFloat64(mRpsPerPod.WithLabelValues("demo", "app-agentic")))
	assert.Equal(t, 1.0, testutil.ToFloat64(mScaleEventsTotal.WithLabelValues("demo", "app-agentic", reasoning.ScaleUp)))
}

func TestObserveReconcile_ReasonLabelDistinguishesScaleUpVsCooldown(t *testing.T) {
	mScaleEventsTotal.Reset()

	observeReconcile("demo", "app-agentic", reasoning.ScaleUp, 0, 0, 0)
	observeReconcile("demo", "app-agentic", reasoning.ScaleUp, 0, 0, 0)
	observeReconcile("demo", "app-agentic", reasoning.CooldownHoldingUp, 0, 0, 0)

	assert.Equal(t, 2.0, testutil.ToFloat64(mScaleEventsTotal.WithLabelValues("demo", "app-agentic", reasoning.ScaleUp)))
	assert.Equal(t, 1.0, testutil.ToFloat64(mScaleEventsTotal.WithLabelValues("demo", "app-agentic", reasoning.CooldownHoldingUp)))
}

func TestObserveClassification_KnownPatternsMapToStableEnum(t *testing.T) {
	mClassifiedPattern.Reset()
	mClassifiedConfidence.Reset()

	observeClassification("demo", "app-agentic", classifier.PatternFlat, "high")
	assert.Equal(t, 1.0, testutil.ToFloat64(mClassifiedPattern.WithLabelValues("demo", "app-agentic")))
	assert.Equal(t, 2.0, testutil.ToFloat64(mClassifiedConfidence.WithLabelValues("demo", "app-agentic")))

	observeClassification("demo", "app-agentic", classifier.PatternSpiky, "medium")
	assert.Equal(t, 3.0, testutil.ToFloat64(mClassifiedPattern.WithLabelValues("demo", "app-agentic")))
	assert.Equal(t, 1.0, testutil.ToFloat64(mClassifiedConfidence.WithLabelValues("demo", "app-agentic")))
}

func TestObserveClassification_UnknownPatternIsIgnored(t *testing.T) {
	mClassifiedPattern.Reset()

	observeClassification("demo", "app-agentic", classifier.PatternFlat, "high")
	before := testutil.ToFloat64(mClassifiedPattern.WithLabelValues("demo", "app-agentic"))

	// Random garbage must not corrupt the gauge.
	observeClassification("demo", "app-agentic", "made_up_pattern", "high")
	after := testutil.ToFloat64(mClassifiedPattern.WithLabelValues("demo", "app-agentic"))

	assert.Equal(t, before, after)
}

func TestObserveClassification_EmptyPatternMapsToZero(t *testing.T) {
	mClassifiedPattern.Reset()

	// Freshly-deployed CR with no classifier output yet — gauge should
	// read zero, not "no series", so the dashboard panel renders.
	observeClassification("demo", "fresh-cr", "", "")
	assert.Equal(t, 0.0, testutil.ToFloat64(mClassifiedPattern.WithLabelValues("demo", "fresh-cr")))
}

func TestObserveForecastFailure_Increments(t *testing.T) {
	mForecastFailuresTotal.Reset()
	observeForecastFailure("demo", "app-agentic")
	observeForecastFailure("demo", "app-agentic")
	assert.Equal(t, 2.0, testutil.ToFloat64(mForecastFailuresTotal.WithLabelValues("demo", "app-agentic")))
}

func TestObserveMetricsUnavailable_Increments(t *testing.T) {
	mMetricsUnavailableTotal.Reset()
	observeMetricsUnavailable("demo", "app-agentic")
	assert.Equal(t, 1.0, testutil.ToFloat64(mMetricsUnavailableTotal.WithLabelValues("demo", "app-agentic")))
}

// TestPatternEnum_HasAllCanonicalPatterns guards against quietly forgetting
// to add a new classifier.Pattern* constant to the enum map; without this,
// the dashboard would silently miss the new pattern's value-mapping.
func TestPatternEnum_HasAllCanonicalPatterns(t *testing.T) {
	required := []string{
		classifier.PatternFlat,
		classifier.PatternPeriodic,
		classifier.PatternSpiky,
		classifier.PatternGradualRamp,
		classifier.PatternDefault,
	}
	for _, p := range required {
		_, ok := patternEnum[p]
		assert.True(t, ok, "patternEnum missing entry for classifier.%s", strings.ToTitle(p))
	}
}
