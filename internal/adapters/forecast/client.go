/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package forecast

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"
)

// Client is the Forecast Service HTTP client.
type Client struct {
	baseURL string
	http    *http.Client
}

// New constructs a Forecast Service client.
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: timeout},
	}
}

// ErrInvalidResponse covers any case where the service returned a 200 with
// a body that violates the contract (missing field, NaN, negative).
// Per design §9, callers should emit `forecast_unavailable` and no-op.
var ErrInvalidResponse = errors.New("forecast: invalid response")

// Recommend posts to /recommend. "auto" PreferredModel is normalised to ""
// so the JSON body omits the field entirely (per design §5 wire-equivalence).
func (c *Client) Recommend(ctx context.Context, req RecommendRequest) (RecommendResponse, error) {
	if req.PreferredModel == "auto" {
		req.PreferredModel = ""
	}

	body, err := json.Marshal(req)
	if err != nil {
		return RecommendResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/recommend", bytes.NewReader(body))
	if err != nil {
		return RecommendResponse{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return RecommendResponse{}, fmt.Errorf("forecast request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return RecommendResponse{}, fmt.Errorf("forecast returned HTTP %d", resp.StatusCode)
	}

	var out RecommendResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return RecommendResponse{}, fmt.Errorf("decode forecast response: %w", err)
	}

	if math.IsNaN(out.PredictedRPS) || math.IsInf(out.PredictedRPS, 0) || out.PredictedRPS < 0 {
		return RecommendResponse{}, fmt.Errorf("%w: predicted_rps=%v", ErrInvalidResponse, out.PredictedRPS)
	}
	if out.ModelUsed == "" {
		return RecommendResponse{}, fmt.Errorf("%w: missing model_used", ErrInvalidResponse)
	}

	return out, nil
}
