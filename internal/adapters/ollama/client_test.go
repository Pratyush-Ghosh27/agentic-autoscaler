/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package ollama_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/ollama"
)

func TestChat_HappyPath(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "choices": [
		    {"message": {"role": "assistant", "content": "Scaling up to keep up with traffic."},
		     "finish_reason": "stop"}
		  ]
		}`))
	}))
	defer srv.Close()

	c := ollama.New(srv.URL, 5*time.Second)
	content, err := c.Chat(context.Background(), ollama.ChatRequest{
		Model: "phi3",
		Messages: []ollama.ChatMessage{
			{Role: "system", Content: "You are observing a Kubernetes autoscaler."},
			{Role: "user", Content: "Why scale up?"},
		},
		MaxTokens: 150,
	})

	require.NoError(t, err)
	assert.Equal(t, "Scaling up to keep up with traffic.", content)

	// Wire body shape.
	assert.Equal(t, "phi3", captured["model"])
	assert.Equal(t, false, captured["stream"])
	assert.Equal(t, float64(150), captured["max_tokens"])
}

func TestChat_404ReturnsErrModelNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"model 'phi9' not found"}`))
	}))
	defer srv.Close()

	c := ollama.New(srv.URL, 1*time.Second)
	_, err := c.Chat(context.Background(), ollama.ChatRequest{Model: "phi9"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ollama.ErrModelNotFound))
}

func TestChat_5xxReturnsGenericError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := ollama.New(srv.URL, 1*time.Second)
	_, err := c.Chat(context.Background(), ollama.ChatRequest{Model: "phi3"})
	require.Error(t, err)
	assert.False(t, errors.Is(err, ollama.ErrModelNotFound))
	assert.Contains(t, err.Error(), "500")
}

func TestChat_TimeoutReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	c := ollama.New(srv.URL, 50*time.Millisecond)
	_, err := c.Chat(context.Background(), ollama.ChatRequest{Model: "phi3"})
	require.Error(t, err)
}

func TestChat_EmptyChoicesReturnsErrEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	c := ollama.New(srv.URL, 1*time.Second)
	_, err := c.Chat(context.Background(), ollama.ChatRequest{Model: "phi3"})
	require.ErrorIs(t, err, ollama.ErrEmptyResponse)
}

func TestChat_EmptyContentReturnsErrEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":""}}]}`))
	}))
	defer srv.Close()

	c := ollama.New(srv.URL, 1*time.Second)
	_, err := c.Chat(context.Background(), ollama.ChatRequest{Model: "phi3"})
	require.ErrorIs(t, err, ollama.ErrEmptyResponse)
}

func TestChat_MalformedJSONReturnsDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{garbage`))
	}))
	defer srv.Close()

	c := ollama.New(srv.URL, 1*time.Second)
	_, err := c.Chat(context.Background(), ollama.ChatRequest{Model: "phi3"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}
