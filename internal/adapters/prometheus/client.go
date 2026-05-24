/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package prometheus is the Controller-side PromQL HTTP client.
// Designed to talk to the kube-prometheus-stack Prometheus over its
// HTTP API. See docs/design.md §5 (hot path) and §6.1 (cold path).
package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client is a thin PromQL HTTP client.
type Client struct {
	baseURL string
	http    *http.Client
}

// New constructs a Client with the given base URL (e.g.
// "http://prometheus.monitoring.svc:9090") and per-request timeout.
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

// Sample is a single (timestamp, value) datum in a range-query response.
type Sample struct {
	Timestamp time.Time
	Value     float64
}

// instantResponse mirrors Prometheus's /api/v1/query JSON.
type instantResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string          `json:"resultType"`
		Result     []instantSample `json:"result"`
	} `json:"data"`
	ErrorType string `json:"errorType,omitempty"`
	Error     string `json:"error,omitempty"`
}

type instantSample struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"` // [timestamp(float), value(string)]
}

// rangeResponse mirrors Prometheus's /api/v1/query_range JSON.
type rangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string        `json:"resultType"`
		Result     []rangeSeries `json:"result"`
	} `json:"data"`
	ErrorType string `json:"errorType,omitempty"`
	Error     string `json:"error,omitempty"`
}

type rangeSeries struct {
	Metric map[string]string `json:"metric"`
	Values [][2]any          `json:"values"`
}

// InstantQuery executes an instant PromQL query and returns the scalar
// value of the first sample. An empty result returns 0.0 with no error
// (caller's choice to treat that as "metrics_unavailable" upstream — see
// design §9).
func (c *Client) InstantQuery(ctx context.Context, query string) (float64, error) {
	u := c.baseURL + "/api/v1/query?" + url.Values{"query": []string{query}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("prometheus request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return 0, fmt.Errorf("prometheus returned HTTP %d", resp.StatusCode)
	}

	var out instantResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, fmt.Errorf("decode prometheus response: %w", err)
	}
	if out.Status != "success" {
		return 0, fmt.Errorf("prometheus reported error: %s", out.Error)
	}
	if len(out.Data.Result) == 0 {
		return 0, nil
	}

	raw, ok := out.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, fmt.Errorf("prometheus value not a string: %v", out.Data.Result[0].Value[1])
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse prometheus value: %w", err)
	}
	return v, nil
}

// RangeQuery executes a range PromQL query and returns the points of
// the first series. Empty result returns nil samples with no error.
func (c *Client) RangeQuery(
	ctx context.Context,
	query string,
	start, end time.Time,
	step time.Duration,
) ([]Sample, error) {
	q := url.Values{
		"query": []string{query},
		"start": []string{strconv.FormatInt(start.Unix(), 10)},
		"end":   []string{strconv.FormatInt(end.Unix(), 10)},
		"step":  []string{strconv.Itoa(int(step.Seconds()))},
	}
	u := c.baseURL + "/api/v1/query_range?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus range request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("prometheus returned HTTP %d", resp.StatusCode)
	}

	var out rangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode prometheus response: %w", err)
	}
	if out.Status != "success" {
		return nil, fmt.Errorf("prometheus reported error: %s", out.Error)
	}
	if len(out.Data.Result) == 0 {
		return nil, nil
	}

	first := out.Data.Result[0]
	samples := make([]Sample, 0, len(first.Values))
	for _, pair := range first.Values {
		ts, ok := pair[0].(float64)
		if !ok {
			return nil, fmt.Errorf("range timestamp not a number: %v", pair[0])
		}
		raw, ok := pair[1].(string)
		if !ok {
			return nil, fmt.Errorf("range value not a string: %v", pair[1])
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("parse range value: %w", err)
		}
		samples = append(samples, Sample{Timestamp: time.Unix(int64(ts), 0), Value: v})
	}
	return samples, nil
}
