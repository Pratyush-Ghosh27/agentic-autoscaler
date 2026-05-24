/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client is the Ollama OpenAI-compatible HTTP client.
type Client struct {
	baseURL string
	http    *http.Client
}

// New constructs an Ollama client.
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: timeout},
	}
}

// Chat posts to /v1/chat/completions and returns the assistant content
// from the first choice. Returns ErrModelNotFound on 404, ErrEmptyResponse
// when no choices or empty content.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (string, error) {
	req.Stream = false // OpenAI-compatible streaming is out of scope for ExplainWorker.

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", ErrModelNotFound
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("ollama returned HTTP %d", resp.StatusCode)
	}

	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode ollama response: %w", err)
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content == "" {
		return "", ErrEmptyResponse
	}
	return out.Choices[0].Message.Content, nil
}
