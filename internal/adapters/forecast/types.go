/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package forecast is the Controller-side client for the Forecast Service.
// See docs/design.md §5 for the wire contract.
package forecast

// RecommendRequest is the body of POST /recommend.
type RecommendRequest struct {
	RpsHistory []float64 `json:"rps_history"`
	WorkloadID string    `json:"workload_id,omitempty"`
	// PreferredModel is "prophet", "linear_extrap", or "auto" / "" to defer.
	// Per design §5, "auto" / null / absent must be wire-equivalent — so the
	// adapter normalises "auto" to "" and the omitempty tag drops it.
	PreferredModel string `json:"preferred_model,omitempty"`
	// Context carries the cold-path-computed scalar features and the
	// 24-bin hourly profile, plus current-time fields supplied by the
	// controller. nil (omitempty) means "no context for this request" —
	// either before the first classification cycle, or because the user
	// engaged the autoscaling.agentic.io/skip-context annotation.
	// See docs/design_v2.md §6.3 (G10 forwarding contract).
	Context *ContextPayload `json:"context,omitempty"`
}

// ContextPayload is the wire-side mirror of v1alpha1.ContextFields plus
// the two current-time fields the controller stamps on every request.
// Field names use snake_case to match the Forecast Service Pydantic
// model.
type ContextPayload struct {
	BaselineRPS        int32   `json:"baseline_rps"`
	PeakP95RPS         int32   `json:"peak_p95_rps"`
	Trend24hSlope      float64 `json:"trend_24h_slope"`
	HourlyProfile      []int32 `json:"hourly_profile"`
	HourlyProfileValid bool    `json:"hourly_profile_valid"`
	// CurrentHourUTC is 0..23. Stamped by the controller at request
	// build time so forecasters can index HourlyProfile.
	CurrentHourUTC int32 `json:"current_hour_utc"`
	// CurrentMinuteUTC is 0..59. Stamped by the controller at request
	// build time for sub-hour interpolation.
	CurrentMinuteUTC int32 `json:"current_minute_utc"`
}

// RecommendResponse is the body returned by POST /recommend.
type RecommendResponse struct {
	PredictedRPS   float64 `json:"predicted_rps"`
	HorizonMinutes int     `json:"horizon_minutes"`
	ModelUsed      string  `json:"model_used"`
}
