/*
Copyright 2026.
*/

package controller_test

import (
	"context"
	"sync"
	"time"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/forecast"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/prometheus"
	controller "github.com/pratyush-ghosh/agentic-autoscaler/internal/controller"
)

// fakePromQuerier returns scripted instant + range responses. Tests mutate
// its fields between reconciles to script different behaviours; the lock
// guards against the test goroutine and the reconcile goroutine racing.
type fakePromQuerier struct {
	mu         sync.Mutex
	instantVal float64
	instantErr error
	rangeVal   []prometheus.Sample
	rangeErr   error
}

func (f *fakePromQuerier) InstantQuery(_ context.Context, _ string) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.instantVal, f.instantErr
}

func (f *fakePromQuerier) RangeQuery(_ context.Context, _ string, _, _ time.Time, _ time.Duration) ([]prometheus.Sample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rangeVal, f.rangeErr
}

// fakeForecaster scripts a /recommend response. It records the last request
// it observed so tests can assert on the wire-level "auto" → "" mapping
// from the reconciler.
type fakeForecaster struct {
	mu      sync.Mutex
	resp    forecast.RecommendResponse
	err     error
	lastReq *forecast.RecommendRequest
}

func (f *fakeForecaster) Recommend(_ context.Context, req forecast.RecommendRequest) (forecast.RecommendResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := req
	f.lastReq = &r
	return f.resp, f.err
}

func (f *fakeForecaster) lastRequest() *forecast.RecommendRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lastReq == nil {
		return nil
	}
	out := *f.lastReq
	return &out
}

// fakeExplainNotifier is a Notify-only sink that remembers the most recent
// request. Used by envtest specs to assert ExplainNotify is wired.
type fakeExplainNotifier struct {
	mu      sync.Mutex
	lastReq *controller.ExplainRequest
	count   int
}

func (f *fakeExplainNotifier) Notify(req controller.ExplainRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := req
	f.lastReq = &r
	f.count++
}

func (f *fakeExplainNotifier) last() *controller.ExplainRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lastReq == nil {
		return nil
	}
	out := *f.lastReq
	return &out
}

func (f *fakeExplainNotifier) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}
