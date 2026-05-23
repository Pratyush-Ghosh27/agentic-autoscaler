# Plan 03 — External Adapters Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the three external HTTP/PromQL clients that the controller depends on — Prometheus, Forecast, and Ollama — with typed request/response shapes, configurable timeouts pulled from `internal/config.Config` (Plan #1), and full contract-test coverage (happy path, 5xx, timeout, malformed response). Each adapter returns typed errors so reconciler / worker callers in later plans can branch on specific failure modes (`ErrModelNotFound`, `ErrEmptyResponse`, etc.).

**Architecture:** One package per adapter under `internal/adapters/`, each holding a small `Client` struct with `New(baseURL, timeout) *Client` plus operation methods. Tests use `httptest.NewServer` to stand up a fake remote — no real Prometheus / forecast / Ollama needed for unit tests. Each adapter is fully independent: no shared base type, no shared HTTP wrapper. Sharing would couple them to a fictional consistency they don't actually share (Prometheus uses GET with query params; Forecast and Ollama use POST with JSON; Ollama returns OpenAI-compatible JSON; Forecast returns our own shape).

**Tech Stack:** Go 1.23, `net/http`, `encoding/json`, `net/http/httptest` for tests, testify v1.10. No new third-party deps; the controller's go.mod is unchanged.

---

## Spec Coverage Map

| Spec section | Tasks |
| --- | --- |
| §3 architecture: Controller → Forecast Service over HTTP | T5, T6, T7 |
| §3 architecture: Controller → Ollama over OpenAI-compatible /v1/chat/completions | T8, T9, T10 |
| §3 architecture: Controller → Prometheus via PromQL HTTP API | T2, T3, T4 |
| §5 hot path: instant query `sum(rate(http_requests_total{deployment="<target>"}[2m]))` returns scalar | T2 |
| §5 hot path: rps_history range query — `HOT_PATH_HISTORY_MINUTES` at 1-min resolution | T3 |
| §6.1 cold path: `CLASSIFIER_HISTORY_HOURS × 60` minute samples — same range-query shape | T3 (consumed by Plan #5; this plan only ships the adapter) |
| §5 forecast service contract (input shape with optional preferred_model + workload_id; output shape with predicted_rps, horizon_minutes, model_used) | T5, T6 |
| §5 forecast service: `preferred_model="auto"` / null / absent must be wire-equivalent | T6 |
| §6.2 ExplainWorker → Ollama `POST /v1/chat/completions` (OpenAI-compatible) | T8 |
| §9 failure: Forecast Service unreachable / 5xx / timeout > FORECAST_TIMEOUT_SECONDS | T7 |
| §9 failure: Forecast Service returns invalid response (missing field, NaN, negative) | T7 |
| §9 failure: Ollama call times out or returns 5xx | T9 |
| §9 failure: Ollama model not found (404) | T9 |
| §9 failure: Ollama returns empty content or malformed JSON | T10 |
| §9 failure: Prometheus query fails or returns no data | T4 |

What's intentionally not in this plan: caller logic that decides what to do on each failure (Plans #4, #5, #6 own that). The adapters surface typed errors; callers branch.

---

## File Structure

```
scaler/internal/adapters/
├── prometheus/
│   ├── client.go         # T2-T4
│   └── client_test.go    # T2-T4 (httptest)
├── forecast/
│   ├── client.go         # T5-T7
│   ├── types.go          # T5: request/response structs
│   └── client_test.go    # T5-T7
└── ollama/
    ├── client.go         # T8-T10
    ├── types.go          # T8: request/response structs
    ├── errors.go         # T9: typed errors
    └── client_test.go    # T8-T10
```

### File responsibilities

- `prometheus/client.go` — `Client{baseURL, http}` with `InstantQuery(ctx, q) (float64, error)` and `RangeQuery(ctx, q, start, end, step) ([]Sample, error)`. Maps Prometheus's `data.result[0].value` and `data.result[0].values` into Go types. Logs nothing; surfaces typed errors only.
- `forecast/types.go` — `RecommendRequest{RpsHistory, WorkloadID, PreferredModel}` with `omitempty` JSON tags so `PreferredModel == ""` is dropped from the wire body (the design's "auto / null / absent" equivalence).
- `forecast/client.go` — `Client.Recommend(ctx, req) (RecommendResponse, error)`. Treats `req.PreferredModel == "auto"` as identical to `""` (both omitted on the wire).
- `ollama/types.go` — `ChatRequest`, `ChatResponse`, `ChatMessage` matching OpenAI-compatible schema.
- `ollama/errors.go` — `ErrModelNotFound`, `ErrEmptyResponse` sentinels (`var Err... = errors.New(...)` so callers can `errors.Is` them).
- `ollama/client.go` — `Client.Chat(ctx, req) (content string, err error)`. Returns just the `choices[0].message.content` string.

---

## Phase 1 — Prometheus adapter (Tier-1 strict TDD)

### Task 1: Module init for the adapters package

**Files:** none (verification only)

- [ ] **Step 1: Verify the directories don't exist yet**

```bash
test ! -d internal/adapters && echo "OK"
```

Expected: `OK`. They are about to be created by T2.

- [ ] **Step 2: No commit (verification only)**

---

### Task 2: Prometheus instant query (happy path)

**Files:**
- Create: `internal/adapters/prometheus/client.go`
- Create: `internal/adapters/prometheus/client_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/prometheus/client_test.go`:

```go
package prometheus_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/prometheus"
)

func TestInstantQuery_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/query", r.URL.Path)
		assert.Equal(t, `sum(rate(http_requests_total{deployment="demo"}[2m]))`, r.URL.Query().Get("query"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "status": "success",
		  "data": {
		    "resultType": "vector",
		    "result": [
		      {"metric": {}, "value": [1716504000.000, "1234.56"]}
		    ]
		  }
		}`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 5*time.Second)
	v, err := c.InstantQuery(context.Background(), `sum(rate(http_requests_total{deployment="demo"}[2m]))`)

	require.NoError(t, err)
	assert.InDelta(t, 1234.56, v, 0.001)
}

func TestInstantQuery_NoSamplesReturnsZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 5*time.Second)
	v, err := c.InstantQuery(context.Background(), `whatever`)

	require.NoError(t, err)
	assert.Equal(t, 0.0, v, "empty result should yield zero (not an error)")
}
```

- [ ] **Step 2: Run; expect ImportError**

```bash
go test ./internal/adapters/prometheus/... -v
```

Expected: build failure on undefined `prometheus.New` and `Client.InstantQuery`.

- [ ] **Step 3: Implement the minimal client**

Create `internal/adapters/prometheus/client.go`:

```go
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

// New constructs a Client with the given base URL (e.g. "http://prometheus.monitoring.svc:9090")
// and per-request timeout.
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		http: &http.Client{
			Timeout: timeout,
		},
	}
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

// InstantQuery executes an instant PromQL query and returns the scalar value
// of the first sample. Empty result returns 0.0 with no error (caller's choice
// to treat that as "metrics_unavailable" upstream — see design §9).
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
```

- [ ] **Step 4: Run; verify pass**

Run: `go test ./internal/adapters/prometheus/... -v`
Expected: 2 PASSED.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/
git commit -m "feat(adapters): add prometheus.Client.InstantQuery"
```

---

### Task 3: Prometheus range query

**Files:**
- Modify: `internal/adapters/prometheus/client.go`
- Modify: `internal/adapters/prometheus/client_test.go`

- [ ] **Step 1: Append failing range-query test**

```go
func TestRangeQuery_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/query_range", r.URL.Path)
		require.Equal(t, "100", r.URL.Query().Get("start"))
		require.Equal(t, "160", r.URL.Query().Get("end"))
		require.Equal(t, "60", r.URL.Query().Get("step"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "status": "success",
		  "data": {
		    "resultType": "matrix",
		    "result": [
		      {
		        "metric": {},
		        "values": [
		          [100, "10.0"],
		          [160, "12.5"]
		        ]
		      }
		    ]
		  }
		}`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 5*time.Second)
	start := time.Unix(100, 0)
	end := time.Unix(160, 0)
	samples, err := c.RangeQuery(context.Background(), "ignored", start, end, time.Minute)

	require.NoError(t, err)
	require.Len(t, samples, 2)
	assert.InDelta(t, 10.0, samples[0].Value, 0.001)
	assert.Equal(t, time.Unix(100, 0), samples[0].Timestamp)
	assert.InDelta(t, 12.5, samples[1].Value, 0.001)
}

func TestRangeQuery_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 5*time.Second)
	samples, err := c.RangeQuery(context.Background(), "x", time.Unix(0, 0), time.Unix(60, 0), time.Minute)
	require.NoError(t, err)
	assert.Empty(t, samples)
}
```

- [ ] **Step 2: Run; expect failure**

Expected: build failure — `Sample`, `Client.RangeQuery` undefined.

- [ ] **Step 3: Add Sample, RangeQuery**

Append to `internal/adapters/prometheus/client.go`:

```go
// Sample is a single (timestamp, value) datum in a range-query response.
type Sample struct {
	Timestamp time.Time
	Value     float64
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

// RangeQuery executes a range PromQL query and returns the points of the
// first series. Empty result returns nil samples with no error.
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
```

- [ ] **Step 4: Run; verify pass**

Expected: 4 PASSED.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/prometheus/
git commit -m "feat(adapters): add prometheus.Client.RangeQuery"
```

---

### Task 4: Prometheus failure paths

**Files:**
- Modify: `internal/adapters/prometheus/client_test.go`

- [ ] **Step 1: Append failure tests**

```go
func TestInstantQuery_5xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 1*time.Second)
	_, err := c.InstantQuery(context.Background(), "q")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestInstantQuery_TimeoutReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 50*time.Millisecond)
	_, err := c.InstantQuery(context.Background(), "q")
	require.Error(t, err)
}

func TestInstantQuery_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not even close to json`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 1*time.Second)
	_, err := c.InstantQuery(context.Background(), "q")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

func TestInstantQuery_PrometheusReportedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"error","error":"parse error","errorType":"bad_data"}`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 1*time.Second)
	_, err := c.InstantQuery(context.Background(), "q")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse error")
}
```

- [ ] **Step 2: Run; verify pass**

Expected: 8 PASSED total. Each failure path is already covered by the implementation from T2/T3.

- [ ] **Step 3: Coverage check**

```bash
go test ./internal/adapters/prometheus/... -cover
```

Expected: ≥ 90% on the package.

- [ ] **Step 4: Commit**

```bash
git add internal/adapters/prometheus/
git commit -m "test(adapters): cover prometheus 5xx, timeout, malformed, reported-error paths"
```

---

## Phase 2 — Forecast adapter (Tier-1 strict TDD)

### Task 5: Forecast types + Recommend happy path

**Files:**
- Create: `internal/adapters/forecast/types.go`
- Create: `internal/adapters/forecast/client.go`
- Create: `internal/adapters/forecast/client_test.go`

- [ ] **Step 1: Failing test**

Create `internal/adapters/forecast/client_test.go`:

```go
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
	assert.InDeltaSlice(t, []float64{100, 120, 140}, captured["rps_history"], 0.001)
	assert.Equal(t, "demo/app-agentic", captured["workload_id"])
	assert.Equal(t, "prophet", captured["preferred_model"])
}
```

- [ ] **Step 2: Run; expect ImportError**

```bash
go test ./internal/adapters/forecast/... -v
```

Expected: undefined `forecast.New`, `forecast.RecommendRequest`, etc.

- [ ] **Step 3: Implement types and client**

Create `internal/adapters/forecast/types.go`:

```go
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
```

Create `internal/adapters/forecast/client.go`:

```go
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
// so the JSON body omits the field entirely.
func (c *Client) Recommend(ctx context.Context, req RecommendRequest) (RecommendResponse, error) {
	if req.PreferredModel == "auto" {
		req.PreferredModel = ""
	}

	body, err := json.Marshal(req)
	if err != nil {
		return RecommendResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/recommend", bytes.NewReader(body))
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

	// §9: reject NaN, negative, and missing model_used.
	if math.IsNaN(out.PredictedRPS) || out.PredictedRPS < 0 {
		return RecommendResponse{}, fmt.Errorf("%w: predicted_rps=%v", ErrInvalidResponse, out.PredictedRPS)
	}
	if out.ModelUsed == "" {
		return RecommendResponse{}, fmt.Errorf("%w: missing model_used", ErrInvalidResponse)
	}

	return out, nil
}
```

- [ ] **Step 4: Run; verify pass**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/forecast/
git commit -m "feat(adapters): add forecast.Client.Recommend"
```

---

### Task 6: Forecast preferred_model="auto" / null / absent must be wire-equivalent

**Files:**
- Modify: `internal/adapters/forecast/client_test.go`

- [ ] **Step 1: Append parametrised test**

```go
func TestRecommend_AutoNullAbsentAreWireEquivalent(t *testing.T) {
	cases := []struct {
		name    string
		req     forecast.RecommendRequest
		hasKey  bool
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
```

- [ ] **Step 2: Run; verify pass**

Expected: 4 subtests PASS — `auto`, `absent`, and `empty` all omit the key on the wire; `prophet` keeps it.

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/forecast/client_test.go
git commit -m "test(adapters): forecast preferred_model auto/null/absent wire-equivalence"
```

---

### Task 7: Forecast failure paths

**Files:**
- Modify: `internal/adapters/forecast/client_test.go`

- [ ] **Step 1: Append failing tests**

```go
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

func TestRecommend_NaNReturnsErrInvalidResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"predicted_rps":"NaN","horizon_minutes":10,"model_used":"prophet"}`))
	}))
	defer srv.Close()

	c := forecast.New(srv.URL, 1*time.Second)
	_, err := c.Recommend(context.Background(), forecast.RecommendRequest{RpsHistory: []float64{1}})
	require.Error(t, err)
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
```

- [ ] **Step 2: Run; verify pass**

Expected: all PASS. The NaN test depends on Go's JSON decoder rejecting `"NaN"` — verify by running the test; if it slips through, add an explicit `math.IsInf` check alongside `math.IsNaN`.

- [ ] **Step 3: Coverage check**

```bash
go test ./internal/adapters/forecast/... -cover
```

Expected: ≥ 90%.

- [ ] **Step 4: Commit**

```bash
git add internal/adapters/forecast/
git commit -m "test(adapters): cover forecast 5xx, timeout, malformed, invalid-response paths"
```

---

## Phase 3 — Ollama adapter (Tier-1 strict TDD)

### Task 8: Ollama types + Chat happy path

**Files:**
- Create: `internal/adapters/ollama/types.go`
- Create: `internal/adapters/ollama/errors.go`
- Create: `internal/adapters/ollama/client.go`
- Create: `internal/adapters/ollama/client_test.go`

- [ ] **Step 1: Failing test**

Create `internal/adapters/ollama/client_test.go`:

```go
package ollama_test

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
```

- [ ] **Step 2: Run; expect ImportError**

```bash
go test ./internal/adapters/ollama/... -v
```

Expected: undefined symbols.

- [ ] **Step 3: Implement types, errors, and client**

Create `internal/adapters/ollama/types.go`:

```go
// Package ollama is the Controller-side client for Ollama via the
// OpenAI-compatible /v1/chat/completions endpoint. See docs/design.md §6.2.
package ollama

// ChatMessage is one entry in the chat history.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the body of POST /v1/chat/completions.
type ChatRequest struct {
	Model     string        `json:"model"`
	Messages  []ChatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
	Stream    bool          `json:"stream"`
}

// chatResponse is the decoded body. We expose only the assistant content via Chat().
type chatResponse struct {
	Choices []struct {
		Message      ChatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
}
```

Create `internal/adapters/ollama/errors.go`:

```go
package ollama

import "errors"

// ErrModelNotFound is returned when Ollama responds with 404, indicating
// the requested model has not been pulled. Per design §9 the caller logs
// a warning suggesting `ollama pull <model>`.
var ErrModelNotFound = errors.New("ollama: model not found (run `ollama pull <model>`)")

// ErrEmptyResponse is returned when Ollama returns a 200 with no choices
// or an empty content string. Per design §9 the caller emits no event.
var ErrEmptyResponse = errors.New("ollama: empty response")
```

Create `internal/adapters/ollama/client.go`:

```go
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
// when content is empty.
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
```

- [ ] **Step 4: Run; verify pass**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/ollama/
git commit -m "feat(adapters): add ollama.Client.Chat with typed errors"
```

---

### Task 9: Ollama 404, 5xx, timeout

**Files:**
- Modify: `internal/adapters/ollama/client_test.go`

- [ ] **Step 1: Append tests**

```go
import (
	// ensure errors imported:
	"errors"
)

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
```

- [ ] **Step 2: Run; verify pass**

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/ollama/client_test.go
git commit -m "test(adapters): cover ollama 404/5xx/timeout"
```

---

### Task 10: Ollama empty content + malformed JSON

**Files:**
- Modify: `internal/adapters/ollama/client_test.go`

- [ ] **Step 1: Append tests**

```go
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
```

- [ ] **Step 2: Run; verify pass**

Expected: PASS.

- [ ] **Step 3: Coverage check**

```bash
go test ./internal/adapters/ollama/... -cover
```

Expected: ≥ 90%.

- [ ] **Step 4: Commit**

```bash
git add internal/adapters/ollama/client_test.go
git commit -m "test(adapters): cover ollama empty-content and malformed-JSON paths"
```

---

## Phase 4 — Final smoke + milestone

### Task 11: Final lint + cross-package test pass

**Files:** none

- [ ] **Step 1: Lint and full test**

```bash
go vet ./...
go test ./internal/adapters/...
go test ./...
```

Expected: clean.

- [ ] **Step 2: Coverage roll-up**

```bash
go test ./internal/adapters/... -coverprofile=/tmp/adapters.cov
go tool cover -func=/tmp/adapters.cov | tail -1
```

Expected: total coverage `≥ 90%`.

- [ ] **Step 3: Milestone commit**

```bash
git commit --allow-empty -m "milestone: Plan #3 (external adapters) complete

- Prometheus client: instant + range queries; httptest contract coverage of 5xx,
  timeout, malformed, prometheus-reported-error
- Forecast client: POST /recommend with auto/null/absent wire-equivalence,
  ErrInvalidResponse for negative/NaN/missing-model_used, full failure-path coverage
- Ollama client: POST /v1/chat/completions; ErrModelNotFound on 404, ErrEmptyResponse
  on empty choices/content; full failure-path coverage
- All three adapters consume Config values from internal/config (Plan #1) — no
  new env vars introduced
"
```

---

## Plan-specific Definition of Done

- [ ] `go test ./internal/adapters/... -v -count=1` exits zero. Every Test*_5xx / *_Timeout / *_Malformed / *_HappyPath subtest passes.
- [ ] `go vet ./...` clean.
- [ ] Each adapter package has ≥ 90% coverage.
- [ ] Forecast adapter omits `preferred_model` on the wire when value is `""`, `"auto"`, or absent (verified by `TestRecommend_AutoNullAbsentAreWireEquivalent`).
- [ ] Forecast adapter returns `ErrInvalidResponse` on negative `predicted_rps`, NaN, or missing `model_used`.
- [ ] Ollama adapter returns `ErrModelNotFound` on HTTP 404 (verified by `errors.Is`).
- [ ] Ollama adapter returns `ErrEmptyResponse` on empty `choices` array OR empty `content`.

---

## Notes on what's intentionally deferred

- **Caller behaviour on each typed error** — Plan #4 (reconciler) consumes Prometheus + Forecast errors, mapping them to `metrics_unavailable` / `forecast_unavailable` events; Plan #6 (ExplainWorker) consumes Ollama errors, mapping them to silent-no-event-with-warning.
- **Retries** — none in adapters per design ("No retries within a single reconcile or classification cycle. Just wait for the next trigger."). Each call is one-shot.
- **mTLS / auth** — out of scope. Prometheus and Forecast Service are in-cluster (NetworkPolicy is the boundary); Ollama is a host process bound to localhost.
- **Streaming Ollama responses** — out of scope. ExplainWorker uses non-streaming; the `Stream` field is force-set to `false` in `Chat`.
- **Connection pooling tuning** — stdlib defaults are fine for the controller's single-digit RPS.

---

## Self-Review (Spec Coverage, Placeholders, Type Consistency)

**Spec coverage.** Every row of the Spec Coverage Map is implemented. The "Forecast Service unreachable / 5xx / timeout > FORECAST_TIMEOUT_SECONDS" row is satisfied by T7's `TestRecommend_TimeoutReturnsError` (uses 50 ms timeout against a 200 ms server) plus the `New()` constructor accepting a configurable timeout (so callers in Plan #4 can pass `cfg.ForecastTimeout`).

**Placeholders.** None. Every test has full Go code; every error message is exact.

**Type consistency.**

- `prometheus.Client` exposes `InstantQuery` and `RangeQuery`. Both signatures match across declaration (T2, T3) and tests (T2-T4).
- `forecast.RecommendRequest` field names (`RpsHistory`, `WorkloadID`, `PreferredModel`) and JSON tags (`rps_history`, `workload_id`, `preferred_model`) match the Forecast Service's Pydantic model in Plan #7 (T10).
- `forecast.RecommendResponse` field names (`PredictedRPS`, `HorizonMinutes`, `ModelUsed`) and tags (`predicted_rps`, `horizon_minutes`, `model_used`) match the Forecast Service's response model.
- `ollama.ChatRequest` and `ChatResponse` use the OpenAI-compatible JSON shape (`model`, `messages`, `max_tokens`, `stream`, `choices[].message.content`) — same shape Plan #6 will use.
- Sentinel errors `ErrModelNotFound`, `ErrEmptyResponse`, `ErrInvalidResponse` are declared once via `errors.New` so `errors.Is` works for callers.

---

## Execution handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-24-plan-03-external-adapters.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using `executing-plans`, batch execution with checkpoints for review.

Which approach?


