/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	"fmt"
	"strings"

	autoscalingv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/api/v1alpha1"
)

// ValidateSpec returns a non-nil error if the spec violates any of the
// bound checks documented in docs/design_v2.md §4. The error message lists
// every problem found, so an operator with a misconfigured CR sees all
// of them at once instead of fixing one and re-applying.
//
// Optional fields (those typed as pointers in v1alpha1) are only checked
// when non-nil. Required fields are assumed satisfied by the kubebuilder
// validation markers and the API server's structural schema.
func ValidateSpec(spec *autoscalingv1alpha1.AgenticAutoscalerSpec) error {
	var problems []string

	// Replica bounds — design §4.
	if spec.MinReplicas != nil && *spec.MinReplicas < 1 {
		problems = append(problems, fmt.Sprintf(
			"minReplicas=%d must be >= 1", *spec.MinReplicas))
	}
	// Strict inequality (F37): the §7 maxStep formula
	// clamp(ceil(log2(peak_to_trough)), 1, maxReplicas - minReplicas)
	// has an empty clamp range when minReplicas == maxReplicas (lower 1,
	// upper 0). Rejecting equality at admission eliminates that
	// degenerate path and forces operators who want a pinned replica
	// count to use rangeSize=1 (max = min + 1).
	if spec.MinReplicas != nil && spec.MaxReplicas != nil &&
		*spec.MaxReplicas <= *spec.MinReplicas {
		problems = append(problems, fmt.Sprintf(
			"maxReplicas=%d must be > minReplicas=%d",
			*spec.MaxReplicas, *spec.MinReplicas))
	}

	// Capacity bounds — design §4.
	if spec.RpsPerPodMin != nil && *spec.RpsPerPodMin < 1 {
		problems = append(problems, fmt.Sprintf(
			"rpsPerPodMin=%d must be >= 1", *spec.RpsPerPodMin))
	}
	if spec.RpsPerPodMin != nil && spec.RpsPerPodMax != nil &&
		*spec.RpsPerPodMin >= *spec.RpsPerPodMax {
		problems = append(problems, fmt.Sprintf(
			"rpsPerPodMin=%d must be < rpsPerPodMax=%d",
			*spec.RpsPerPodMin, *spec.RpsPerPodMax))
	}

	// maxStepSize bounds — design §4 (only when non-nil).
	if spec.MaxStepSize != nil {
		if *spec.MaxStepSize < 1 {
			problems = append(problems, fmt.Sprintf(
				"maxStepSize=%d must be >= 1", *spec.MaxStepSize))
		}
		if spec.MinReplicas != nil && spec.MaxReplicas != nil {
			rangeSize := *spec.MaxReplicas - *spec.MinReplicas
			if *spec.MaxStepSize > rangeSize {
				problems = append(problems, fmt.Sprintf(
					"maxStepSize=%d must be <= maxReplicas - minReplicas (=%d)",
					*spec.MaxStepSize, rangeSize))
			}
		}
	}

	// Cooldowns — design §4 (only when non-nil; zero means "no cooldown").
	if spec.ScaleUpCooldownSeconds != nil && *spec.ScaleUpCooldownSeconds < 0 {
		problems = append(problems, fmt.Sprintf(
			"scaleUpCooldownSeconds=%d must be >= 0", *spec.ScaleUpCooldownSeconds))
	}
	if spec.ScaleDownCooldownSeconds != nil && *spec.ScaleDownCooldownSeconds < 0 {
		problems = append(problems, fmt.Sprintf(
			"scaleDownCooldownSeconds=%d must be >= 0", *spec.ScaleDownCooldownSeconds))
	}

	// preferredForecaster — design §4 (only when non-nil).
	if spec.PreferredForecaster != nil {
		switch *spec.PreferredForecaster {
		case "prophet", "linear_extrap", "gbdt_quantile", "auto":
			// accepted
		default:
			problems = append(problems, fmt.Sprintf(
				"preferredForecaster=%q must be one of prophet, linear_extrap, gbdt_quantile, auto",
				*spec.PreferredForecaster))
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("agenticautoscaler validation failed: %s", strings.Join(problems, "; "))
}
