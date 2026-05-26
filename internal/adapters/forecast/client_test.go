/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package forecast_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/forecast"
)

func TestRecommend_HappyPath(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/recommend", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"predicted_rps": 1450.5, "horizon_minutes": 10, "model_used": "prophet"}`))
	}))
	defer srv.Close()

	c := forecast.New(srv.URL, 5*time.Second)
	resp, err := c.Recommend(context.Background(), forecast.RecommendRequest{
		RpsHistory:     []float64{100, 120, 140},
		WorkloadID:     "demo/app-agentic",
		PreferredModel: "prophet",
	})

	require.NoError(t, err)
	assert.InDelta(t, 1450.5, resp.PredictedRPS, 0.001)
	assert.Equal(t, 10, resp.HorizonMinutes)
	assert.Equal(t, "prophet", resp.ModelUsed)

	// Wire body shape.
	hist, _ := captured["rps_history"].([]any)
	require.Len(t, hist, 3)
	assert.InDelta(t, 100.0, hist[0].(float64), 0.001)
	assert.Equal(t, "demo/app-agentic", captured["workload_id"])
	assert.Equal(t, "prophet", captured["preferred_model"])
}

func TestRecommend_AutoNullAbsentAreWireEquivalent(t *testing.T) {
	cases := []struct {
		name   string
		req    forecast.RecommendRequest
		hasKey bool
	}{
		{name: "auto", req: forecast.RecommendRequest{RpsHistory: []float64{1}, PreferredModel: "auto"}, hasKey: false},
		{name: "absent", req: forecast.RecommendRequest{RpsHistory: []float64{1}}, hasKey: false},
		{name: "empty", req: forecast.RecommendRequest{RpsHistory: []float64{1}, PreferredModel: ""}, hasKey: false},
		{name: "prophet", req: forecast.RecommendRequest{RpsHistory: []float64{1}, PreferredModel: "prophet"}, hasKey: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				require.NoError(t, json.Unmarshal(body, &captured))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"predicted_rps":100,"horizon_minutes":10,"model_used":"linear_extrap"}`))
			}))
			defer srv.Close()

			c := forecast.New(srv.URL, 1*time.Second)
			_, err := c.Recommend(context.Background(), tc.req)
			require.NoError(t, err)

			_, present := captured["preferred_model"]
			assert.Equal(t, tc.hasKey, present, "preferred_model presence on wire")
		})
	}
}

func TestRecommend_5xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := forecast.New(srv.URL, 1*time.Second)
	_, err := c.Recommend(context.Background(), forecast.RecommendRequest{RpsHistory: []float64{1}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestRecommend_TimeoutReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	c := forecast.New(srv.URL, 50*time.Millisecond)
	_, err := c.Recommend(context.Background(), forecast.RecommendRequest{RpsHistory: []float64{1}})
	require.Error(t, err)
}

func TestRecommend_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`<not json>`))
	}))
	defer srv.Close()

	c := forecast.New(srv.URL, 1*time.Second)
	_, err := c.Recommend(context.Background(), forecast.RecommendRequest{RpsHistory: []float64{1}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

func TestRecommend_NegativePredictedReturnsErrInvalidResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"predicted_rps":-1.0,"horizon_minutes":10,"model_used":"prophet"}`))
	}))
	defer srv.Close()

	c := forecast.New(srv.URL, 1*time.Second)
	_, err := c.Recommend(context.Background(), forecast.RecommendRequest{RpsHistory: []float64{1}})
	require.Error(t, err)
	assert.ErrorIs(t, err, forecast.ErrInvalidResponse)
}

// TestRecommend_ContextOnWire pins that ContextPayload is serialised
// under the "context" key with the snake_case field names that the
// Forecast Service Pydantic model expects (G10).
func TestRecommend_ContextOnWire(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"predicted_rps":150,"horizon_minutes":10,"model_used":"prophet"}`))
	}))
	defer srv.Close()

	c := forecast.New(srv.URL, 1*time.Second)
	_, err := c.Recommend(context.Background(), forecast.RecommendRequest{
		RpsHistory: []float64{100, 110, 120},
		Context: &forecast.ContextPayload{
			BaselineRPS:        50,
			PeakP95RPS:         200,
			Trend24hSlope:      0.5,
			HourlyProfile:      []int32{10, 12, 14, 18, 22, 30, 50, 80, 100, 120, 140, 150, 150, 145, 140, 130, 110, 95, 80, 60, 40, 25, 15, 10},
			HourlyProfileValid: true,
			CurrentHourUTC:     14,
			CurrentMinuteUTC:   30,
		},
	})
	require.NoError(t, err)

	ctx, ok := captured["context"].(map[string]any)
	require.True(t, ok, "context should be a JSON object on the wire, got: %v", captured["context"])
	assert.Equal(t, 50.0, ctx["baseline_rps"])
	assert.Equal(t, 200.0, ctx["peak_p95_rps"])
	assert.InDelta(t, 0.5, ctx["trend_24h_slope"], 0.001)
	assert.Equal(t, true, ctx["hourly_profile_valid"])
	assert.Equal(t, 14.0, ctx["current_hour_utc"])
	assert.Equal(t, 30.0, ctx["current_minute_utc"])
	hp, ok := ctx["hourly_profile"].([]any)
	require.True(t, ok)
	assert.Len(t, hp, 24)
	assert.Equal(t, 10.0, hp[0])
	assert.Equal(t, 150.0, hp[11])
}

// TestRecommend_ContextOmittedWhenNil pins that a nil Context is
// dropped from the wire (omitempty) so existing requests are
// byte-identical to v1 and the Forecast Service treats it as absent.
func TestRecommend_ContextOmittedWhenNil(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"predicted_rps":150,"horizon_minutes":10,"model_used":"prophet"}`))
	}))
	defer srv.Close()

	c := forecast.New(srv.URL, 1*time.Second)
	_, err := c.Recommend(context.Background(), forecast.RecommendRequest{
		RpsHistory: []float64{100, 110, 120},
	})
	require.NoError(t, err)

	_, present := captured["context"]
	assert.False(t, present, "nil Context must be dropped from the wire (omitempty)")
}

func TestRecommend_MissingModelUsedReturnsErrInvalidResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"predicted_rps":100,"horizon_minutes":10}`))
	}))
	defer srv.Close()

	c := forecast.New(srv.URL, 1*time.Second)
	_, err := c.Recommend(context.Background(), forecast.RecommendRequest{RpsHistory: []float64{1}})
	require.ErrorIs(t, err, forecast.ErrInvalidResponse)
}
