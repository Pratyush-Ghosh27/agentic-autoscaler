package reasoning

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAllTokens_StableSet locks down the full token inventory.
// Adding a new token requires updating this test in the same PR.
func TestAllTokens_StableSet(t *testing.T) {
	expected := map[string]string{
		"ScaleUp":             "scale_up",
		"ScaleDown":           "scale_down",
		"NoChange":            "no_change",
		"StepCappedUp":        "step_capped_up",
		"StepCappedDown":      "step_capped_down",
		"CooldownHoldingUp":   "cooldown_holding_up",
		"CooldownHoldingDown": "cooldown_holding_down",
		"MaxReplicasBinding":  "max_replicas_binding",
		"MinReplicasBinding":  "min_replicas_binding",
		"KillSwitched":        "kill_switched",
		"ConflictDetected":    "conflict_detected",
		"ForecastUnavailable": "forecast_unavailable",
		"MetricsUnavailable":  "metrics_unavailable",
		"PatternClassified":   "pattern_classified",
		"PatternUnknown":      "pattern_unknown",
		"ScaleExplained":      "scale_explained",
	}

	got := AllTokens()
	assert.Equal(t, expected, got, "reasoning-token inventory drift; update both the constants and this test in the same commit")
}

// TestBindingTokenConstants pins the wire format of the two G13 binding
// tokens. decision.ClampRecommended returns these string literals
// directly (avoiding an import cycle); changing either string here without
// updating decision.go is a silent regression.
func TestBindingTokenConstants(t *testing.T) {
	assert.Equal(t, "max_replicas_binding", MaxReplicasBinding)
	assert.Equal(t, "min_replicas_binding", MinReplicasBinding)
}

// TestPascalReason_AllTokensHaveMapping ensures every snake_case token in
// AllTokens() has a non-empty PascalCase mapping. This catches the
// silent-empty-Reason regression that would otherwise fire only when a
// new token is added without a corresponding pascalMap entry.
func TestPascalReason_AllTokensHaveMapping(t *testing.T) {
	for name, snake := range AllTokens() {
		pascal := PascalReason(snake)
		assert.NotEmpty(t, pascal, "token %s (%s) has no PascalCase mapping", name, snake)
		// PascalCase must start with an uppercase letter.
		assert.Regexp(t, `^[A-Z]`, pascal,
			"PascalReason(%q) must be PascalCase (got %q)", snake, pascal)
	}
}

// TestPascalReason_SpecificMappings pins each entry in the §5 step-10
// mapping table so a typo in either the constant value or the pascalMap
// surfaces as a test failure rather than as a silent runtime regression.
func TestPascalReason_SpecificMappings(t *testing.T) {
	cases := []struct{ snake, pascal string }{
		{"scale_up", "ScaleUp"},
		{"scale_down", "ScaleDown"},
		{"no_change", "NoChange"},
		{"step_capped_up", "StepCappedUp"},
		{"step_capped_down", "StepCappedDown"},
		{"cooldown_holding_up", "CooldownHoldingUp"},
		{"cooldown_holding_down", "CooldownHoldingDown"},
		{"max_replicas_binding", "MaxReplicasBinding"},
		{"min_replicas_binding", "MinReplicasBinding"},
		{"kill_switched", "KillSwitched"},
		{"conflict_detected", "ConflictDetected"},
		{"forecast_unavailable", "ForecastUnavailable"},
		{"metrics_unavailable", "MetricsUnavailable"},
		{"pattern_classified", "PatternClassified"},
		{"pattern_unknown", "PatternUnknown"},
		{"scale_explained", "ScaleExplained"},
	}
	for _, tc := range cases {
		t.Run(tc.snake, func(t *testing.T) {
			assert.Equal(t, tc.pascal, PascalReason(tc.snake))
		})
	}
}

// TestPascalReason_UnknownTokenReturnsSelf — defensive contract: a typo
// in caller code should never produce an empty Reason field. We return
// the input unchanged rather than panicking or returning "" so an
// unmapped token at least surfaces as a noticeable Reason value
// (e.g. "future_token" instead of "").
func TestPascalReason_UnknownTokenReturnsSelf(t *testing.T) {
	assert.Equal(t, "unknown_token", PascalReason("unknown_token"))
	assert.Equal(t, "", PascalReason(""))
}

func TestAnnotation_KillSwitchKey(t *testing.T) {
	assert.Equal(t, "autoscaling.agentic.io/kill-switch", AnnotationKillSwitch)
}

func TestAnnotation_ReclassifyKey(t *testing.T) {
	assert.Equal(t, "autoscaling.agentic.io/reclassify", AnnotationReclassify)
}
