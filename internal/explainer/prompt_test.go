/*
Copyright 2026.
*/

package explainer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/controller"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/explainer"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/reasoning"
)

// baseRequest returns a fully-populated ExplainRequest representing a
// "happy path" scale_up. Tests mutate fields to exercise conditionals.
// G18 fields are set to realistic non-zero values so the kitchen-sink
// Long-term context line renders meaningful numbers in every test.
func baseRequest() controller.ExplainRequest {
	return controller.ExplainRequest{
		Namespace:             "demo",
		Name:                  "app-agentic",
		Reason:                reasoning.ScaleUp,
		CurrentReplicas:       4,
		RecommendedReplicas:   6,
		UnboundedRecommended:  6,
		TargetReplicas:        6,
		MaxReplicas:           10,
		MinReplicas:           2,
		CurrentRPS:            800.5,
		PredictedRPS:          1200.3,
		HorizonMinutes:        10,
		ModelUsed:             "prophet",
		Pattern:               "periodic",
		Confidence:            "high",
		BaselineRPS:           400,
		PeakP95RPS:            1200,
		HourlyProfileValid:    true,
		EffectiveCooldownUp:   60,
		EffectiveCooldownDown: 300,
		EffectiveMaxStep:      3,
	}
}

// -----------------------------------------------------------------------
// Happy path: every field appears, no clipped line, system message fixed.
// -----------------------------------------------------------------------

func TestBuildPrompt_HappyPath(t *testing.T) {
	sys, user := explainer.BuildPrompt(baseRequest())

	assert.Equal(t, explainer.SystemMessage, sys)
	assert.Contains(t, user, "Traffic pattern: periodic (confidence: high)")
	assert.Contains(t, user, "Current RPS: 800.5")
	assert.Contains(t, user, "Predicted RPS (10 min ahead): 1200.3")
	assert.Contains(t, user, "4 → 6 replicas (scale_up)")
	assert.Contains(t, user, "Forecasting model: prophet")
	assert.Contains(t, user, "scaleUpCooldown=60s")
	assert.Contains(t, user, "scaleDownCooldown=300s")
	assert.Contains(t, user, "maxStep=3")
	assert.NotContains(t, user, "limited by maxStep",
		"plain scale_up has no clipped line")
}

func TestBuildPrompt_SystemMessageIsFixed(t *testing.T) {
	a := baseRequest()
	b := baseRequest()
	b.Reason = reasoning.ScaleDown
	b.Pattern = ""
	b.RecommendedReplicas = 2
	b.TargetReplicas = 2

	sysA, _ := explainer.BuildPrompt(a)
	sysB, _ := explainer.BuildPrompt(b)
	require.Equal(t, sysA, sysB,
		"system message must not vary across requests")
}

// -----------------------------------------------------------------------
// Conditional: traffic-pattern line.
// -----------------------------------------------------------------------

func TestBuildPrompt_OmitsPatternWhenEmpty(t *testing.T) {
	req := baseRequest()
	req.Pattern = ""
	_, user := explainer.BuildPrompt(req)
	assert.NotContains(t, user, "Traffic pattern:")
}

func TestBuildPrompt_OmitsPatternWhenDefault(t *testing.T) {
	req := baseRequest()
	req.Pattern = "default"
	_, user := explainer.BuildPrompt(req)
	assert.NotContains(t, user, "Traffic pattern:")
}

func TestBuildPrompt_IncludesPatternWhenFlat(t *testing.T) {
	// "flat" is a valid classified pattern — keep it visible.
	req := baseRequest()
	req.Pattern = "flat"
	req.Confidence = "medium"
	_, user := explainer.BuildPrompt(req)
	assert.Contains(t, user, "Traffic pattern: flat (confidence: medium)")
}

// -----------------------------------------------------------------------
// Long-term context line — F12 gate is Pattern != "" (NOT != "default").
// -----------------------------------------------------------------------

func TestBuildPrompt_IncludesLongTermContextWhenPatternSet(t *testing.T) {
	req := baseRequest()
	req.BaselineRPS = 400
	req.PeakP95RPS = 1200
	_, user := explainer.BuildPrompt(req)
	assert.Contains(t, user, "Long-term context: baseline 400 rps, p95 1200 rps")
}

func TestBuildPrompt_IncludesLongTermContextWhenPatternIsDefault(t *testing.T) {
	// F12: classifier *has* run (Pattern="default"); baseline/p95 are real.
	// The Traffic-pattern line is still suppressed (Pattern == "default"
	// case), but the Long-term context line must render.
	req := baseRequest()
	req.Pattern = "default"
	req.BaselineRPS = 50
	req.PeakP95RPS = 200
	_, user := explainer.BuildPrompt(req)
	assert.Contains(t, user, "Long-term context: baseline 50 rps, p95 200 rps")
	assert.NotContains(t, user, "Traffic pattern: default",
		"Traffic-pattern line is still gated on Pattern != \"default\"")
}

func TestBuildPrompt_OmitsLongTermContextWhenPatternEmpty(t *testing.T) {
	req := baseRequest()
	req.Pattern = ""
	req.BaselineRPS = 400
	req.PeakP95RPS = 1200
	_, user := explainer.BuildPrompt(req)
	assert.NotContains(t, user, "Long-term context:")
}

func TestBuildPrompt_LongTermContextWithZeroValues(t *testing.T) {
	// F12 rationale: a workload with genuinely zero traffic must still
	// have the line rendered (zero is a measurement, not absence).
	req := baseRequest()
	req.Pattern = "flat"
	req.BaselineRPS = 0
	req.PeakP95RPS = 0
	_, user := explainer.BuildPrompt(req)
	assert.Contains(t, user, "Long-term context: baseline 0 rps, p95 0 rps")
}

func TestBuildPrompt_ClosingWordingMentionsLongTermBaseline(t *testing.T) {
	_, user := explainer.BuildPrompt(baseRequest())
	assert.Contains(t, user,
		"Explain why this decision was made and what the traffic data suggests "+
			"relative to the workload's long-term baseline.")
}

// -----------------------------------------------------------------------
// Conditional: clipped line.
// -----------------------------------------------------------------------

func TestBuildPrompt_IncludesClippedLineForStepCappedUp(t *testing.T) {
	req := baseRequest()
	req.Reason = reasoning.StepCappedUp
	req.RecommendedReplicas = 8
	req.TargetReplicas = 6
	req.EffectiveMaxStep = 2
	_, user := explainer.BuildPrompt(req)
	assert.Contains(t, user, "limited by maxStep")
	assert.Contains(t, user, "computed 8 replicas")
	assert.Contains(t, user, "moved only to 6")
	assert.Contains(t, user, "cap: 2 replicas per reconcile")
}

func TestBuildPrompt_IncludesClippedLineForStepCappedDown(t *testing.T) {
	req := baseRequest()
	req.Reason = reasoning.StepCappedDown
	req.RecommendedReplicas = 2
	req.TargetReplicas = 4
	req.EffectiveMaxStep = 2
	_, user := explainer.BuildPrompt(req)
	assert.Contains(t, user, "limited by maxStep")
}

func TestBuildPrompt_NoClippedLineForUncappedScaleDown(t *testing.T) {
	req := baseRequest()
	req.Reason = reasoning.ScaleDown
	_, user := explainer.BuildPrompt(req)
	assert.NotContains(t, user, "limited by maxStep")
}

func TestBuildPrompt_NoClippedLineForCooldownReason(t *testing.T) {
	req := baseRequest()
	req.Reason = reasoning.CooldownHoldingUp
	_, user := explainer.BuildPrompt(req)
	assert.NotContains(t, user, "limited by maxStep",
		"cooldown is not a step cap")
}

// -----------------------------------------------------------------------
// TrimContent.
// -----------------------------------------------------------------------

func TestTrimContent_ShorterThanLimit(t *testing.T) {
	short := "This is fine."
	assert.Equal(t, short, explainer.TrimContent(short, 500))
}

func TestTrimContent_ExactlyLimit(t *testing.T) {
	exact := strings.Repeat("y", 500)
	assert.Equal(t, exact, explainer.TrimContent(exact, 500))
}

func TestTrimContent_LongerThanLimit(t *testing.T) {
	long := strings.Repeat("x", 600)
	out := explainer.TrimContent(long, 500)
	assert.Len(t, out, 500)
	assert.True(t, strings.HasSuffix(out, "..."))
}

func TestTrimContent_DegenerateTinyLimit(t *testing.T) {
	// maxLen ≤ 3 cannot fit "..." — fall back to a hard truncate.
	out := explainer.TrimContent("hello", 2)
	assert.Equal(t, "he", out)
}
