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
}

// RecommendResponse is the body returned by POST /recommend.
type RecommendResponse struct {
	PredictedRPS   float64 `json:"predicted_rps"`
	HorizonMinutes int     `json:"horizon_minutes"`
	ModelUsed      string  `json:"model_used"`
}
