// Package reasoning centralises the reasoning tokens emitted as
// Kubernetes Event reasons by the controller. See docs/design.md
// §5, §6, and §9 for context.
package reasoning

// Reasoning tokens. Used as the `reason` field of K8s Events.
// String values are stable wire format — Grafana panels and runbooks
// match on these strings. Renaming a value is a breaking change.
const (
	// Hot-path reconciler — replica-changing.
	ScaleUp        = "scale_up"
	ScaleDown      = "scale_down"
	NoChange       = "no_change"
	StepCappedUp   = "step_capped_up"
	StepCappedDown = "step_capped_down"

	// Hot-path reconciler — replica-blocking.
	CooldownHoldingUp   = "cooldown_holding_up"
	CooldownHoldingDown = "cooldown_holding_down"
	KillSwitched        = "kill_switched"
	ConflictDetected    = "conflict_detected"

	// Hot-path reconciler — failure paths.
	ForecastUnavailable = "forecast_unavailable"
	MetricsUnavailable  = "metrics_unavailable"

	// Cold-path workers.
	PatternClassified = "pattern_classified"
	PatternUnknown    = "pattern_unknown"
	ScaleExplained    = "scale_explained"
)

// CR annotation keys used by operators to signal the controller.
const (
	AnnotationKillSwitch = "autoscaling.agentic.io/kill-switch"
	AnnotationReclassify = "autoscaling.agentic.io/reclassify"
)

// AllTokens returns the full inventory of reasoning tokens, keyed by
// their Go constant name. The test suite snapshots this map; adding a
// new token requires updating both the constant block and the test.
func AllTokens() map[string]string {
	return map[string]string{
		"ScaleUp":             ScaleUp,
		"ScaleDown":           ScaleDown,
		"NoChange":            NoChange,
		"StepCappedUp":        StepCappedUp,
		"StepCappedDown":      StepCappedDown,
		"CooldownHoldingUp":   CooldownHoldingUp,
		"CooldownHoldingDown": CooldownHoldingDown,
		"KillSwitched":        KillSwitched,
		"ConflictDetected":    ConflictDetected,
		"ForecastUnavailable": ForecastUnavailable,
		"MetricsUnavailable":  MetricsUnavailable,
		"PatternClassified":   PatternClassified,
		"PatternUnknown":      PatternUnknown,
		"ScaleExplained":      ScaleExplained,
	}
}
