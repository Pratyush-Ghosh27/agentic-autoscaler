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
func baseRequest() controller.ExplainRequest {
	return controller.ExplainRequest{
		Namespace:             "demo",
		Name:                  "app-agentic",
		Reason:                reasoning.ScaleUp,
		CurrentReplicas:       4,
		RecommendedReplicas:   6,
		TargetReplicas:        6,
		CurrentRPS:            800.5,
		PredictedRPS:          1200.3,
		HorizonMinutes:        10,
		ModelUsed:             "prophet",
		Pattern:               "periodic",
		Confidence:            "high",
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
