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
// bound checks documented in docs/design.md §4. The error message lists
// every problem found, so an operator with a misconfigured CR sees all
// of them at once instead of fixing one and re-applying.
//
// Optional fields (those typed as pointers in v1alpha1) are only checked
// when non-nil. Required fields are assumed satisfied by the kubebuilder
// validation markers and the API server's structural schema.
func ValidateSpec(spec *autoscalingv1alpha1.AgenticAutoscalerSpec) error {
	var problems []string

	// Future tasks T3-T7 append rules below.

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("agenticautoscaler validation failed: %s", strings.Join(problems, "; "))
}
