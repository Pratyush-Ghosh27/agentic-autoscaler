// Package reasoning centralises the reasoning tokens emitted as
// Kubernetes Event reasons by the controller. See docs/design_v2.md
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

	// Hot-path reconciler — CRD-bound binding constraints. Tentatively set
	// in decision.ClampRecommended (step 5); may be overwritten by step 6
	// (step_capped_*) or step 7 (cooldown_holding_*) per design_v2.md §5
	// precedence rules 1-4. When carried through to step 10, signals that
	// the CRD's [minReplicas, maxReplicas] bounds are the binding constraint
	// — the operator should treat this as a capacity-planning signal.
	MaxReplicasBinding = "max_replicas_binding"
	MinReplicasBinding = "min_replicas_binding"

	KillSwitched     = "kill_switched"
	ConflictDetected = "conflict_detected"

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
		"MaxReplicasBinding":  MaxReplicasBinding,
		"MinReplicasBinding":  MinReplicasBinding,
		"KillSwitched":        KillSwitched,
		"ConflictDetected":    ConflictDetected,
		"ForecastUnavailable": ForecastUnavailable,
		"MetricsUnavailable":  MetricsUnavailable,
		"PatternClassified":   PatternClassified,
		"PatternUnknown":      PatternUnknown,
		"ScaleExplained":      ScaleExplained,
	}
}

// pascalMap is the canonical snake_case → PascalCase mapping for the
// K8s Event Reason field. The full table lives in design_v2.md §5
// step 10 (E2 condenses it to a one-liner once all callers have been
// migrated to use PascalReason()). G22/F39.
//
// PascalCase is the K8s ecosystem convention for the Reason field
// (`kubectl describe`, `kubectl get events`, Grafana alert rules).
// The snake_case form remains the canonical identifier in code (we
// already use it as the constant value and as the prefix in the event
// message body for log searchability).
var pascalMap = map[string]string{
	ScaleUp:             "ScaleUp",
	ScaleDown:           "ScaleDown",
	NoChange:            "NoChange",
	StepCappedUp:        "StepCappedUp",
	StepCappedDown:      "StepCappedDown",
	CooldownHoldingUp:   "CooldownHoldingUp",
	CooldownHoldingDown: "CooldownHoldingDown",
	MaxReplicasBinding:  "MaxReplicasBinding",
	MinReplicasBinding:  "MinReplicasBinding",
	KillSwitched:        "KillSwitched",
	ConflictDetected:    "ConflictDetected",
	ForecastUnavailable: "ForecastUnavailable",
	MetricsUnavailable:  "MetricsUnavailable",
	PatternClassified:   "PatternClassified",
	PatternUnknown:      "PatternUnknown",
	ScaleExplained:      "ScaleExplained",
}

// PascalReason returns the PascalCase K8s Event Reason for the given
// snake_case reasoning token. Unknown tokens are returned unchanged
// (defensive: a typo at the call-site should never silently produce
// an empty Reason field; an unmapped value at least surfaces as a
// noticeable, debuggable Reason). G22/F39.
func PascalReason(snake string) string {
	if p, ok := pascalMap[snake]; ok {
		return p
	}
	return snake
}
