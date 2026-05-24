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

func TestAnnotation_KillSwitchKey(t *testing.T) {
	assert.Equal(t, "autoscaling.agentic.io/kill-switch", AnnotationKillSwitch)
}

func TestAnnotation_ReclassifyKey(t *testing.T) {
	assert.Equal(t, "autoscaling.agentic.io/reclassify", AnnotationReclassify)
}
