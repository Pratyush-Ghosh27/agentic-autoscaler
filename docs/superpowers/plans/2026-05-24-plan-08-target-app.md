# Plan 08 — Target App Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the instrumented Go HTTP server that the controlled experiment scales. Single binary, single image; deployed twice as `app-agentic` (managed by our controller) and `app-hpa` (managed by a standard K8s HPA) under identical traffic. Exposes `/work` (semaphore-bounded; 503 under overload), `/metrics` (Prometheus histogram + status-labeled counter), `/healthz`, and `/readyz`.

**Architecture:** A separate Go module under `target-app/` so the binary doesn't drag in `controller-runtime` / `client-go`. Pure standard library `net/http` plus `prometheus/client_golang`. The server lives in `internal/server/`; `cmd/target-app/main.go` is a thin wiring layer. Concurrency is bounded by a buffered channel-based semaphore. The histogram exposes per-request latency in seconds with buckets covering 1 ms to 10 s; the counter is labeled by `status` (200, 503).

**Tech Stack:** Go 1.23, `net/http`, `prometheus/client_golang` v1.20+, testify v1.10. Single Go module that joins the existing workspace via `go.work`.

---

## Spec Coverage Map

| Spec section | Tasks |
| --- | --- |
| §2 in scope: target application instrumented with request duration histogram + status-labeled counter, semaphore-bounded concurrency returning 503 under overload | T3, T5, T6, T7, T8, T9 |
| §3 architecture: target deployments share an identical image, deployed under two names | T1, T13 (the image artifact) |
| §11 risks (R8): app-agentic and app-hpa must be byte-identical except for ownership | T13 (image is identical; manifest differences live in Plan #10) |
| Strategy doc §7.2 plan 8 gates: 503 under load above semaphore limit; well-formed Prometheus exposition; histogram covers 1 ms to 10 s; /healthz under saturation; /readyz on downstream failure | T9, T5, T6, T3, T4 |

What's intentionally not in this plan: the `app-agentic` and `app-hpa` Deployment manifests (Plan #10), the HPA manifest targeting `app-hpa` (Plan #10), the AgenticAutoscaler CR targeting `app-agentic` (Plan #10), the k6 scenarios driving load (Plan #9). This plan ships a runnable image only.

---

## File Structure

```
scaler/
├── go.work                                          # T15: ./target-app added
└── target-app/
    ├── go.mod                                       # T1: separate module
    ├── go.sum                                       # T1
    ├── Dockerfile                                   # T13: multi-stage
    ├── .dockerignore                                # T13
    ├── README.md                                    # T16
    ├── cmd/target-app/main.go                       # T1 skeleton; T11 augments
    └── internal/server/
        ├── server.go                                # T2-T9: server type + handlers
        ├── server_test.go                           # T2-T9: tests adjacent
        ├── config.go                                # T11: server-side env-var loader
        └── config_test.go                           # T11
```

### File responsibilities

- `cmd/target-app/main.go` — load config, build server, start `http.Server`; nothing else.
- `internal/server/server.go` — `Server` struct holding the semaphore, histogram, counter, work duration. Constructor + `Handler() http.Handler`. Each route is a method on `Server`.
- `internal/server/config.go` — `LoadConfig() Config` reading `TARGET_PORT`, `TARGET_CONCURRENCY`, `TARGET_WORK_DURATION_MS`, `TARGET_WORK_JITTER_MS`. Defaults: 8080, 8, 50, 30.

The target-app config loader is intentionally not the controller's `internal/config/`. They are different binaries with different env vars; sharing a package would be premature coupling.

---

## Phase 0 — Module bootstrap

### Task 1: Create the target-app Go module

**Files:**
- Create: `target-app/go.mod`
- Create: `target-app/cmd/target-app/main.go` (skeleton only)

- [ ] **Step 1: Create the directory and module**

```bash
mkdir -p target-app/cmd/target-app
mkdir -p target-app/internal/server
cd target-app
go mod init github.com/pratyush-ghosh/agentic-autoscaler/target-app
cd ..
```

- [ ] **Step 2: Add prometheus_client and testify**

```bash
cd target-app
go get github.com/prometheus/client_golang/prometheus@v1.20.5
go get github.com/prometheus/client_golang/prometheus/promhttp@v1.20.5
go get github.com/stretchr/testify@v1.10.0
go mod tidy
cd ..
```

- [ ] **Step 3: Create the main.go skeleton (T11 wires config later)**

Create `target-app/cmd/target-app/main.go`:

```go
// target-app is the instrumented HTTP server scaled by the agentic
// autoscaler's controlled experiment. See docs/design.md §3.
package main

import (
	"log"
	"net/http"

	"github.com/pratyush-ghosh/agentic-autoscaler/target-app/internal/server"
)

func main() {
	srv := server.New(server.DefaultConfig())
	addr := ":8080"
	log.Printf("target-app listening on %s", addr)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
```

This will not compile yet — `server.New`, `server.DefaultConfig`, and `Server.Handler` don't exist. T2 introduces them via TDD.

- [ ] **Step 4: Verify go.mod is well-formed**

```bash
cd target-app && go mod tidy && cd ..
```

Expected: clean exit, `go.sum` populated.

- [ ] **Step 5: Commit**

```bash
git add target-app/
git commit -m "feat(target-app): bootstrap separate Go module"
```

---

## Phase 1 — Server skeleton + healthz + readyz (Tier-1 strict TDD)

### Task 2: Server type + /healthz handler

**Files:**
- Create: `target-app/internal/server/server.go`
- Create: `target-app/internal/server/server_test.go`

- [ ] **Step 1: Write the failing test FIRST**

Create `target-app/internal/server/server_test.go`:

```go
package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pratyush-ghosh/agentic-autoscaler/target-app/internal/server"
)

func TestHealthz_ReturnsOK(t *testing.T) {
	srv := server.New(server.DefaultConfig())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "ok")
}
```

- [ ] **Step 2: Run; verify it fails**

```bash
cd target-app && go test ./internal/server/...
```

Expected: build failure — `server.New`, `server.DefaultConfig`, `Server.Handler` undefined.

- [ ] **Step 3: Implement the minimal server**

Create `target-app/internal/server/server.go`:

```go
// Package server is the HTTP server for the target-app load target.
// See docs/design.md §3 for the controlled experiment context.
package server

import (
	"net/http"
)

// Config controls server behaviour.
type Config struct {
	Concurrency       int
	WorkDurationMS    int
	WorkJitterMS      int
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		Concurrency:    8,
		WorkDurationMS: 50,
		WorkJitterMS:   30,
	}
}

// Server is the instrumented HTTP server.
type Server struct {
	cfg Config
}

// New constructs a Server with the given config.
func New(cfg Config) *Server {
	return &Server{cfg: cfg}
}

// Handler returns the http.Handler exposing all routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
```

- [ ] **Step 4: Run; verify it passes**

```bash
go test ./internal/server/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ..
git add target-app/
git commit -m "feat(target-app): add server with /healthz"
```

---

### Task 3: /readyz handler with optional dependency check

**Files:**
- Modify: `target-app/internal/server/server.go`
- Modify: `target-app/internal/server/server_test.go`

- [ ] **Step 1: Append failing tests**

Append to `target-app/internal/server/server_test.go`:

```go
func TestReadyz_DefaultReturnsOK(t *testing.T) {
	srv := server.New(server.DefaultConfig())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestReadyz_FailingDependencyReturns503(t *testing.T) {
	srv := server.New(server.DefaultConfig())
	srv.SetReady(false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestReadyz_RecoversToReady(t *testing.T) {
	srv := server.New(server.DefaultConfig())
	srv.SetReady(false)
	srv.SetReady(true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}
```

- [ ] **Step 2: Run; expect failure**

```bash
cd target-app && go test ./internal/server/...
```

Expected: build failure — `Server.SetReady` undefined.

- [ ] **Step 3: Add ready state and the handler**

In `target-app/internal/server/server.go`:

Add `sync/atomic` to imports. Modify `Server` struct to:

```go
type Server struct {
	cfg   Config
	ready atomic.Bool
}
```

In `New`:

```go
func New(cfg Config) *Server {
	s := &Server{cfg: cfg}
	s.ready.Store(true)
	return s
}
```

Add the setter:

```go
// SetReady toggles the readiness probe response. Used by tests and by
// any future graceful-shutdown path.
func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
}
```

In `Handler`, register the route:

```go
mux.HandleFunc("/readyz", s.handleReadyz)
```

Add the handler:

```go
func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if !s.ready.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"not_ready"}`))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ready"}`))
}
```

- [ ] **Step 4: Run; verify pass**

```bash
go test ./internal/server/...
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
cd ..
git add target-app/
git commit -m "feat(target-app): add /readyz with toggleable readiness state"
```

---

## Phase 2 — /metrics + histogram + counter (Tier-1 strict TDD)

### Task 4: /metrics endpoint with histogram + counter registered

**Files:**
- Modify: `target-app/internal/server/server.go`
- Modify: `target-app/internal/server/server_test.go`

The histogram and counter live on the `Server` struct and are registered with a private `prometheus.Registry` so each Server has isolated metrics — important because `server_test.go` instantiates many Servers in a single test run.

- [ ] **Step 1: Append failing test**

Append to `target-app/internal/server/server_test.go`:

```go
import (
	// ...existing imports plus:
	"strings"
)

func TestMetrics_ExposesHistogramAndCounter(t *testing.T) {
	srv := server.New(server.DefaultConfig())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)

	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "target_app_request_duration_seconds")
	assert.Contains(t, body, "target_app_requests_total")
	// histogram metadata line
	assert.True(t, strings.Contains(body, "# TYPE target_app_request_duration_seconds histogram"))
	// counter metadata line
	assert.True(t, strings.Contains(body, "# TYPE target_app_requests_total counter"))
}

func TestMetrics_HistogramBucketsCover1msTo10s(t *testing.T) {
	srv := server.New(server.DefaultConfig())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	srv.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	// 1 ms = 0.001s bucket should be present.
	assert.Contains(t, body, `le="0.001"`)
	// 10 s bucket should be present.
	assert.Contains(t, body, `le="10"`)
}
```

- [ ] **Step 2: Run; expect 404 from the /metrics route (not yet wired)**

```bash
cd target-app && go test ./internal/server/...
```

Expected: TestMetrics_* tests FAIL (404 returned). Other tests still PASS.

- [ ] **Step 3: Add prometheus client + register the metrics**

In `target-app/internal/server/server.go`, expand the imports:

```go
import (
	"net/http"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)
```

Modify `Server` struct:

```go
type Server struct {
	cfg       Config
	ready     atomic.Bool
	registry  *prometheus.Registry
	histogram *prometheus.HistogramVec
	counter   *prometheus.CounterVec
}
```

Replace `New`:

```go
func New(cfg Config) *Server {
	reg := prometheus.NewRegistry()

	histogram := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "target_app_request_duration_seconds",
			Help: "End-to-end request duration in seconds.",
			Buckets: []float64{
				0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1,
				0.25, 0.5, 1, 2.5, 5, 10,
			},
		},
		[]string{"path"},
	)

	counter := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "target_app_requests_total",
			Help: "Total number of requests, labeled by status.",
		},
		[]string{"path", "status"},
	)

	reg.MustRegister(histogram, counter)

	s := &Server{
		cfg:       cfg,
		registry:  reg,
		histogram: histogram,
		counter:   counter,
	}
	s.ready.Store(true)
	return s
}
```

Register the route in `Handler`:

```go
mux.Handle("/metrics", promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{Registry: s.registry}))
```

- [ ] **Step 4: Run; verify pass**

```bash
go test ./internal/server/...
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
cd ..
git add target-app/
git commit -m "feat(target-app): add /metrics with histogram + counter (1 ms-10 s buckets)"
```

---

## Phase 3 — /work handler with semaphore + 503 (Tier-1 strict TDD)

### Task 5: /work happy path — semaphore acquired, work simulated, response

**Files:**
- Modify: `target-app/internal/server/server.go`
- Modify: `target-app/internal/server/server_test.go`

- [ ] **Step 1: Failing happy-path test**

Append to `target-app/internal/server/server_test.go`:

```go
import (
	// add:
	"time"
)

func TestWork_HappyPathReturns200(t *testing.T) {
	cfg := server.Config{Concurrency: 1, WorkDurationMS: 10, WorkJitterMS: 0}
	srv := server.New(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/work", nil)
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"work":"done"`)
}

func TestWork_HistogramObservedAfterRequest(t *testing.T) {
	cfg := server.Config{Concurrency: 1, WorkDurationMS: 10, WorkJitterMS: 0}
	srv := server.New(cfg)

	// drive one request
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/work", nil)
	srv.Handler().ServeHTTP(rec, req)

	// scrape /metrics
	mrec := httptest.NewRecorder()
	mreq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	srv.Handler().ServeHTTP(mrec, mreq)

	body := mrec.Body.String()
	assert.Contains(t, body, `target_app_request_duration_seconds_count{path="/work"} 1`)
}

func TestWork_CounterIncrementedWithStatus200(t *testing.T) {
	cfg := server.Config{Concurrency: 1, WorkDurationMS: 5, WorkJitterMS: 0}
	srv := server.New(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/work", nil)
	srv.Handler().ServeHTTP(rec, req)

	mrec := httptest.NewRecorder()
	mreq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	srv.Handler().ServeHTTP(mrec, mreq)

	body := mrec.Body.String()
	assert.Contains(t, body, `target_app_requests_total{path="/work",status="200"} 1`)
}

// avoid "imported and not used" if time is unused yet
var _ = time.Now
```

- [ ] **Step 2: Run; expect 404**

```bash
cd target-app && go test ./internal/server/...
```

Expected: the three new tests FAIL (404).

- [ ] **Step 3: Add semaphore + work handler**

In `target-app/internal/server/server.go`:

Expand imports:

```go
import (
	// add:
	"fmt"
	"math/rand/v2"
	"strconv"
	"time"
)
```

Add a semaphore field to `Server`:

```go
type Server struct {
	cfg       Config
	ready     atomic.Bool
	registry  *prometheus.Registry
	histogram *prometheus.HistogramVec
	counter   *prometheus.CounterVec
	sem       chan struct{}
}
```

Initialize in `New` (right before the final `return s`):

```go
s.sem = make(chan struct{}, cfg.Concurrency)
```

Register the route in `Handler`:

```go
mux.HandleFunc("/work", s.handleWork)
```

Add the work handler:

```go
func (s *Server) handleWork(w http.ResponseWriter, _ *http.Request) {
	start := time.Now()
	defer func() {
		s.histogram.WithLabelValues("/work").Observe(time.Since(start).Seconds())
	}()

	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		s.counter.WithLabelValues("/work", strconv.Itoa(http.StatusServiceUnavailable)).Inc()
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprint(w, `{"work":"rejected","reason":"overloaded"}`)
		return
	}

	dur := time.Duration(s.cfg.WorkDurationMS) * time.Millisecond
	if s.cfg.WorkJitterMS > 0 {
		jitter := rand.IntN(s.cfg.WorkJitterMS)  //nolint:gosec — non-cryptographic jitter
		dur += time.Duration(jitter) * time.Millisecond
	}
	time.Sleep(dur)

	s.counter.WithLabelValues("/work", strconv.Itoa(http.StatusOK)).Inc()
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, `{"work":"done"}`)
}
```

- [ ] **Step 4: Remove the placeholder `var _ = time.Now`**

The test file imported `time` defensively. Remove the placeholder line `var _ = time.Now`.

- [ ] **Step 5: Run; verify pass**

```bash
go test ./internal/server/...
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
cd ..
git add target-app/
git commit -m "feat(target-app): add /work with semaphore + histogram + counter"
```

---

### Task 6: 503 path — sustained burst above semaphore limit

**Files:**
- Modify: `target-app/internal/server/server_test.go`

- [ ] **Step 1: Append the failing 503 test**

Append to `target-app/internal/server/server_test.go`:

```go
import (
	// add:
	"sync"
	"sync/atomic"
)

func TestWork_BurstAboveSemaphoreLimit_Returns503(t *testing.T) {
	const concurrency = 2
	const burst = 20
	cfg := server.Config{Concurrency: concurrency, WorkDurationMS: 100, WorkJitterMS: 0}
	srv := server.New(cfg)
	handler := srv.Handler()

	var oks, rejects int32
	var wg sync.WaitGroup
	wg.Add(burst)
	for range burst {
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/work", nil)
			handler.ServeHTTP(rec, req)
			switch rec.Code {
			case http.StatusOK:
				atomic.AddInt32(&oks, 1)
			case http.StatusServiceUnavailable:
				atomic.AddInt32(&rejects, 1)
			}
		}()
	}
	wg.Wait()

	// Of 20 concurrent requests against a 2-slot semaphore with 100ms work,
	// at most ~2-4 can be in flight at any moment. Most are rejected.
	assert.GreaterOrEqual(t, int(oks), 1, "at least one request should succeed")
	assert.GreaterOrEqual(t, int(rejects), burst-concurrency*2, "most concurrent requests should reject")
	assert.Equal(t, burst, int(oks)+int(rejects), "every request should resolve to 200 or 503")
}

func TestWork_503CounterLabeledCorrectly(t *testing.T) {
	cfg := server.Config{Concurrency: 0, WorkDurationMS: 5, WorkJitterMS: 0}
	srv := server.New(cfg)
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/work", nil)
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	mrec := httptest.NewRecorder()
	mreq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	handler.ServeHTTP(mrec, mreq)

	body := mrec.Body.String()
	assert.Contains(t, body, `target_app_requests_total{path="/work",status="503"} 1`)
}
```

- [ ] **Step 2: Run**

```bash
cd target-app && go test ./internal/server/... -v -run TestWork_Burst -run TestWork_503Counter
```

Expected: both PASS. The implementation from T5 already returns 503 when the semaphore is full; T6 only verifies the labelled counter and burst behaviour.

The `concurrency: 0` case in `TestWork_503CounterLabeledCorrectly` is the key assertion: a zero-capacity buffered channel always blocks on send, so the `default` branch in `handleWork`'s select fires immediately and we get 503 every time. This makes for a deterministic 503 test without timing dependencies.

- [ ] **Step 3: Commit**

```bash
cd ..
git add target-app/
git commit -m "test(target-app): cover 503 burst behaviour and counter labeling"
```

---

## Phase 4 — Configurability + main wiring

### Task 7: Server config loader

**Files:**
- Create: `target-app/internal/server/config.go`
- Create: `target-app/internal/server/config_test.go`

- [ ] **Step 1: Failing test for config loader**

Create `target-app/internal/server/config_test.go`:

```go
package server_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pratyush-ghosh/agentic-autoscaler/target-app/internal/server"
)

func TestLoadConfig_Defaults(t *testing.T) {
	cfg := server.LoadConfig()
	assert.Equal(t, 8, cfg.Concurrency)
	assert.Equal(t, 50, cfg.WorkDurationMS)
	assert.Equal(t, 30, cfg.WorkJitterMS)
}

func TestLoadConfig_Overrides(t *testing.T) {
	t.Setenv("TARGET_CONCURRENCY", "4")
	t.Setenv("TARGET_WORK_DURATION_MS", "100")
	t.Setenv("TARGET_WORK_JITTER_MS", "0")

	cfg := server.LoadConfig()
	assert.Equal(t, 4, cfg.Concurrency)
	assert.Equal(t, 100, cfg.WorkDurationMS)
	assert.Equal(t, 0, cfg.WorkJitterMS)
}

func TestLoadConfig_NegativeOrInvalidFallsToDefault(t *testing.T) {
	t.Setenv("TARGET_CONCURRENCY", "-1")          // invalid
	t.Setenv("TARGET_WORK_DURATION_MS", "garbage") // unparseable

	cfg := server.LoadConfig()
	assert.Equal(t, 8, cfg.Concurrency, "negative value should fall to default")
	assert.Equal(t, 50, cfg.WorkDurationMS, "garbage should fall to default")
}
```

- [ ] **Step 2: Run; expect failure**

```bash
cd target-app && go test ./internal/server/... -run TestLoadConfig
```

Expected: build failure — `LoadConfig` undefined.

- [ ] **Step 3: Implement LoadConfig**

Create `target-app/internal/server/config.go`:

```go
package server

import (
	"os"
	"strconv"
)

// LoadConfig reads target-app config from env vars, falling back to
// DefaultConfig() values for anything missing or unparseable.
func LoadConfig() Config {
	cfg := DefaultConfig()
	cfg.Concurrency = envIntOrDefault("TARGET_CONCURRENCY", cfg.Concurrency)
	cfg.WorkDurationMS = envIntOrDefault("TARGET_WORK_DURATION_MS", cfg.WorkDurationMS)
	cfg.WorkJitterMS = envIntOrDefault("TARGET_WORK_JITTER_MS", cfg.WorkJitterMS)
	return cfg
}

func envIntOrDefault(name string, def int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if v < 0 {
		return def
	}
	return v
}
```

Note: `Concurrency=0` is allowed (it makes /work always 503, which is useful for tests like TestWork_503CounterLabeledCorrectly). Negative values are rejected and silently fall to default.

- [ ] **Step 4: Run; verify pass**

```bash
go test ./internal/server/... -run TestLoadConfig
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ..
git add target-app/
git commit -m "feat(target-app): add env-var config loader"
```

---

### Task 8: Wire LoadConfig into main + add port env var

**Files:**
- Modify: `target-app/cmd/target-app/main.go`

- [ ] **Step 1: Replace cmd/target-app/main.go**

```go
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/pratyush-ghosh/agentic-autoscaler/target-app/internal/server"
)

func main() {
	cfg := server.LoadConfig()
	srv := server.New(cfg)

	port := os.Getenv("TARGET_PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

	log.Printf("target-app starting: addr=%s concurrency=%d work_duration_ms=%d work_jitter_ms=%d",
		addr, cfg.Concurrency, cfg.WorkDurationMS, cfg.WorkJitterMS)

	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
```

- [ ] **Step 2: Build and smoke-run**

```bash
cd target-app
go build -o /tmp/target-app ./cmd/target-app
TARGET_PORT=18080 /tmp/target-app &
sleep 1
curl -sf http://localhost:18080/healthz
curl -sf http://localhost:18080/readyz
curl -sf http://localhost:18080/work
curl -sf http://localhost:18080/metrics | head -20
kill %1
wait 2>/dev/null
```

Expected:
- `/healthz` → `{"status":"ok"}`
- `/readyz` → `{"status":"ready"}`
- `/work` → `{"work":"done"}` (or `rejected` if very unlucky on timing; rare with default concurrency=8)
- `/metrics` → Prometheus text including `target_app_request_duration_seconds_count{path="/work"} 1` after the prior /work call

- [ ] **Step 3: Commit**

```bash
cd ..
git add target-app/
git commit -m "feat(target-app): wire LoadConfig and TARGET_PORT into main"
```

---

## Phase 5 — Containerization + workspace integration

### Task 9: Multi-stage Dockerfile

**Files:**
- Create: `target-app/Dockerfile`
- Create: `target-app/.dockerignore`

- [ ] **Step 1: Create .dockerignore**

Create `target-app/.dockerignore`:

```
*.test
*.out
coverage.txt
.idea/
.vscode/
*~
*.swp
```

- [ ] **Step 2: Create Dockerfile**

Create `target-app/Dockerfile`:

```dockerfile
# syntax=docker/dockerfile:1.7

FROM golang:1.23-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' \
    -o /out/target-app ./cmd/target-app

FROM gcr.io/distroless/static:nonroot

COPY --from=builder /out/target-app /target-app

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/target-app"]
```

- [ ] **Step 3: Build and smoke-run the container**

```bash
cd target-app
docker build -t agentic-target-app:dev .
docker run --rm -d --name target-smoke -p 18080:8080 agentic-target-app:dev
sleep 1
curl -sf http://localhost:18080/healthz
curl -sf http://localhost:18080/work
curl -sf http://localhost:18080/metrics | grep target_app_request_duration_seconds_count
docker stop target-smoke
```

Expected: same outputs as T8's local run; the /metrics scrape shows at least 1 request observed.

- [ ] **Step 4: Commit**

```bash
cd ..
git add target-app/
git commit -m "feat(target-app): add distroless multi-stage Dockerfile"
```

---

### Task 10: Add target-app to go.work

**Files:**
- Modify: `go.work`

- [ ] **Step 1: Update go.work**

Open `go.work`. Replace its contents with:

```
go 1.23

use (
	.
	./target-app
)
```

- [ ] **Step 2: Verify both modules resolve**

```bash
go env GOWORK
go build ./...                    # builds the controller
go build ./target-app/...         # builds target-app
go test ./internal/config/... ./target-app/internal/server/...
```

Expected: every command exits zero.

- [ ] **Step 3: Commit**

```bash
git add go.work
git commit -m "chore: add target-app to go.work"
```

---

### Task 11: README + milestone

**Files:**
- Create: `target-app/README.md`

- [ ] **Step 1: Create README.md**

Create `target-app/README.md`:

```markdown
# target-app

Instrumented HTTP server used by the agentic autoscaler's controlled
experiment. Deployed twice as `app-agentic` (managed by the operator)
and `app-hpa` (managed by a standard K8s HPA) under identical traffic,
so the two scalers can be compared on identical workloads.

## Endpoints

| Path | Behaviour |
|---|---|
| `GET /work` | Acquires a semaphore slot, sleeps `TARGET_WORK_DURATION_MS ± TARGET_WORK_JITTER_MS`, returns 200. Returns 503 immediately if no slot is available. |
| `GET /healthz` | Always returns 200 `{"status":"ok"}`. |
| `GET /readyz` | Returns 200 by default; 503 if `SetReady(false)` has been called (test hook). |
| `GET /metrics` | Prometheus exposition: `target_app_request_duration_seconds` (histogram, 1 ms-10 s buckets, label: `path`) and `target_app_requests_total` (counter, labels: `path`, `status`). |

## Configuration

| Env var | Default | Notes |
|---|---|---|
| `TARGET_PORT` | `8080` | Port to listen on. |
| `TARGET_CONCURRENCY` | `8` | Semaphore size. Concurrent /work requests above this return 503. |
| `TARGET_WORK_DURATION_MS` | `50` | Base simulated work duration per /work call. |
| `TARGET_WORK_JITTER_MS` | `30` | Random jitter added on top of base duration. |

## Build

```bash
cd target-app
go build ./...
go test ./...
docker build -t agentic-target-app:dev .
```
```

- [ ] **Step 2: Commit and milestone**

```bash
git add target-app/README.md
git commit -m "docs(target-app): add README"

git commit --allow-empty -m "milestone: Plan #8 (target app) complete

- /work with semaphore-bounded concurrency, 503 under overload
- /metrics with histogram (1 ms-10 s buckets) and status-labeled counter
- /healthz, /readyz with toggleable readiness state
- env-var configurable concurrency, work duration, work jitter, port
- distroless container image
- joined to go.work alongside the controller module
"
```

---

## Plan-specific Definition of Done

- [ ] `cd target-app && go test ./... -v -count=1` exits zero. Every TestWork_* passes.
- [ ] `cd target-app && go vet ./...` clean.
- [ ] `cd target-app && docker build -t agentic-target-app:dev .` succeeds.
- [ ] Container started with `docker run -p 18080:8080 agentic-target-app:dev`:
  - `GET /healthz` returns 200 `{"status":"ok"}` within 1 s of starting.
  - `GET /work` returns 200 `{"work":"done"}`.
  - `GET /metrics` returns Prometheus text containing `target_app_request_duration_seconds` histogram with `le="0.001"` and `le="10"` buckets, plus `target_app_requests_total{path="/work",status="200"}`.
- [ ] With `TARGET_CONCURRENCY=0`, every `GET /work` returns 503 (deterministic 503 check).
- [ ] With `TARGET_CONCURRENCY=2` and 20 concurrent /work requests, at least 1 returns 200 and at least 16 return 503; total = 20.
- [ ] `go.work` includes both `.` and `./target-app`.

---

## Notes on what's intentionally deferred

- **Deployment manifests for `app-agentic` and `app-hpa`** — Plan #10. Both manifests reference the image built here; they'll differ only on `metadata.name`, `metadata.labels`, `spec.selector.matchLabels`, and `spec.template.metadata.labels`.
- **HPA targeting `app-hpa`** — Plan #10.
- **AgenticAutoscaler CR targeting `app-agentic`** — Plan #10.
- **k6 load scenarios** — Plan #9.
- **Memory and CPU limits** — Plan #10's manifest. The image makes no assumptions about resource shape.
- **Graceful shutdown** — out of scope. The current `http.ListenAndServe` exits hard on SIGTERM. If observed flakiness in nightly E2E proves problematic, a small `signal.NotifyContext` wrap in `main.go` is a Plan #11 follow-on.

---

## Self-Review (Spec Coverage, Placeholders, Type Consistency)

**Spec coverage.**

- §2 instrumented histogram + status-labeled counter + semaphore-bounded concurrency returning 503 → T4, T5, T6, T9.
- Strategy doc §7.2 plan 8 gates: 503 under load (T6), well-formed Prometheus exposition (T4), histogram covers 1 ms-10 s (T4), /healthz under saturation (always 200, doesn't touch the semaphore — T2), /readyz on downstream failure (T3).
- Image is a single artifact deployed twice (T9 produces `agentic-target-app:dev`).

**Placeholders.** None. Every test has full Go code; every command has expected output.

**Type consistency.**

- `Config` field names used in `DefaultConfig` (T2), `LoadConfig` (T7), and tests (T2-T7) match: `Concurrency`, `WorkDurationMS`, `WorkJitterMS`.
- `Server` method names — `New`, `Handler`, `SetReady` — match across all tasks.
- Histogram metric name `target_app_request_duration_seconds` and counter `target_app_requests_total` are the same string everywhere (T4 declares them; T5/T6/T9 assert against them).
- Counter label values: `path` (`/work`) and `status` (`200`, `503`) are consistent across producer (T5) and consumers (T5, T6).

---

## Execution handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-24-plan-08-target-app.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using `executing-plans`, batch execution with checkpoints for review.

Which approach?


