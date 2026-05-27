/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import "testing"

// TestClassifiedParamsContextRoundTrip pins the ContextFields shape that
// the cold path writes and the hot path forwards verbatim. See
// docs/design_v2.md §4 status fields and §6.1 step 6.5 for computation.
func TestClassifiedParamsContextRoundTrip(t *testing.T) {
	cp := ClassifiedParams{
		Pattern: "periodic",
		Context: &ContextFields{
			BaselineRPS:        50,
			PeakP95RPS:         200,
			Trend24hSlope:      0.5,
			HourlyProfile:      []int32{10, 12, 14, 18, 22, 30, 50, 80, 100, 120, 140, 150, 150, 145, 140, 130, 110, 95, 80, 60, 40, 25, 15, 10},
			HourlyProfileValid: true,
		},
	}
	if cp.Pattern != "periodic" {
		t.Errorf("Pattern round-trip failed: got %q, want %q", cp.Pattern, "periodic")
	}
	if cp.Context == nil {
		t.Fatal("Context must be non-nil after assignment")
	}
	if len(cp.Context.HourlyProfile) != 24 {
		t.Fatalf("HourlyProfile length = %d, want 24", len(cp.Context.HourlyProfile))
	}
	if cp.Context.BaselineRPS != 50 {
		t.Errorf("BaselineRPS round-trip failed: got %d, want 50", cp.Context.BaselineRPS)
	}
	if cp.Context.PeakP95RPS != 200 {
		t.Errorf("PeakP95RPS round-trip failed: got %d, want 200", cp.Context.PeakP95RPS)
	}
	if cp.Context.Trend24hSlope != 0.5 {
		t.Errorf("Trend24hSlope round-trip failed: got %v, want 0.5", cp.Context.Trend24hSlope)
	}
	if !cp.Context.HourlyProfileValid {
		t.Error("HourlyProfileValid round-trip failed")
	}
}

// TestClassifiedParamsContextOptional pins that Context being nil is a
// valid state (cold start, before the first classification cycle).
func TestClassifiedParamsContextOptional(t *testing.T) {
	cp := ClassifiedParams{Pattern: "default"}
	if cp.Pattern != "default" {
		t.Errorf("Pattern round-trip failed: got %q, want %q", cp.Pattern, "default")
	}
	if cp.Context != nil {
		t.Errorf("zero-value ClassifiedParams must have nil Context, got: %+v", cp.Context)
	}
}

// TestAgenticAutoscalerStatus_UnboundedRecommendedFieldExists pins the
// Plan 15 / G13 status field that surfaces the pre-clamp forecaster ask.
// When this exceeds Spec.MaxReplicas the CRD bound is the binding
// constraint; the new MaxReplicasBinding reasoning token (Task 4) and
// the unboundedRecommended-in-event-message change (Task 7) carry the
// same signal downstream. See docs/design_v2.md §5 step 5.
func TestAgenticAutoscalerStatus_UnboundedRecommendedFieldExists(t *testing.T) {
	s := AgenticAutoscalerStatus{
		RecommendedReplicas:  10,
		UnboundedRecommended: 15,
	}
	if s.UnboundedRecommended != 15 {
		t.Errorf("UnboundedRecommended round-trip failed: got %d, want 15",
			s.UnboundedRecommended)
	}
	if s.RecommendedReplicas != 10 {
		t.Errorf("RecommendedReplicas round-trip failed: got %d, want 10",
			s.RecommendedReplicas)
	}
}
