# Plan 09 — k6 Load Scenarios Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver four k6 load-generation scenarios (ramp, steady, spiky, bursty) that drive identical RPS patterns to both `app-agentic` and `app-hpa` deployments. Each scenario must pass its own `check()` assertions in `--vus=1` dry-run mode against a local httptest server. Durations and RPS profiles are configurable via k6 env vars.

**Architecture:** Each scenario is a self-contained k6 JavaScript file exporting `options` and a `default` function. A shared helper (`k6/lib/targets.js`) constructs the dual-target URL list from env vars (`TARGET_AGENTIC_URL`, `TARGET_HPA_URL`). The `--vus=1` dry-run uses a local test harness (`k6/lib/testserver.go`) that serves `/work` with the same contract as the target-app from Plan #8.

**Tech Stack:** k6 (latest), JavaScript ES6 modules, Go 1.23 (httptest for dry-run validation).

---

## Spec Coverage Map

| Strategy doc section | Tasks |
| --- | --- |
| §7.1 Plan 9: k6/scenarios/{ramp,steady,spiky,bursty}.js | T3, T4, T5, T6 |
| §7.2 Plan 9: `--vus=1` dry-run against httptest | T2 (server), T7 (dry-run verify) |
| §7.2 Plan 9: durations/RPS configurable via k6 env vars | T3-T6 (all use `__ENV`) |
| §7.2 Plan 9: both targets receive byte-equivalent request streams | T3-T6 (shared target helper) |
| §9 Makefile: `make k6-ramp`, `make k6-spiky`, `make k6-steady`, `make k6-bursty` | Plan #11 (Makefile); this plan provides the scripts |

---

## File Structure

```
scaler/k6/
├── lib/
│   ├── targets.js            # T1: shared dual-target URL construction
│   └── testserver.go         # T2: Go httptest server for dry-run validation
├── scenarios/
│   ├── ramp.js               # T3: linear ramp from 0 → peak → 0
│   ├── steady.js             # T4: constant RPS for duration
│   ├── spiky.js              # T5: periodic bursts at fixed intervals
│   └── bursty.js             # T6: random burst pattern
└── dry-run_test.go           # T7: Go test invoking k6 --vus=1 per scenario
```

### File responsibilities

- `lib/targets.js` — exports `getTargets()` returning `[{url: agenticURL}, {url: hpaURL}]` read from `__ENV.TARGET_AGENTIC_URL` and `__ENV.TARGET_HPA_URL`. Falls back to `http://localhost:8080` for dry-run.
- `lib/testserver.go` — a Go `httptest.Server` that responds to `POST /work` with a 200 after a configurable delay (or 503 if overloaded). Used by the dry-run test to validate k6 scenarios in CI without a full cluster.
- `scenarios/ramp.js` — stages: `[{target: RAMP_RPS_PEAK, duration: RAMP_UP_DURATION}, {target: RAMP_RPS_PEAK, duration: RAMP_HOLD_DURATION}, {target: 0, duration: RAMP_DOWN_DURATION}]`. Sends to both targets via batch.
- `scenarios/steady.js` — constant `STEADY_RPS` for `STEADY_DURATION`.
- `scenarios/spiky.js` — alternates between `SPIKE_BASE_RPS` and `SPIKE_PEAK_RPS` every `SPIKE_INTERVAL` seconds.
- `scenarios/bursty.js` — random bursts of `BURST_SIZE` requests at random intervals within `[BURST_MIN_INTERVAL, BURST_MAX_INTERVAL]`.

---

## Phase 1 — Shared library

### Task 1: targets.js helper

**Files:**
- Create: `k6/lib/targets.js`

- [ ] **Step 1: Write targets.js**

```javascript
// Shared dual-target URL construction for k6 scenarios.
// Both deployments receive identical request streams.

export function getTargets() {
  const agenticURL = __ENV.TARGET_AGENTIC_URL || "http://localhost:8080";
  const hpaURL = __ENV.TARGET_HPA_URL || "http://localhost:8081";
  return { agentic: agenticURL, hpa: hpaURL };
}

export function workURL(baseURL) {
  return `${baseURL}/work`;
}
```

- [ ] **Step 2: Commit**

```bash
git add k6/lib/targets.js
git commit -m "feat(k6): shared targets.js helper for dual-target URLs"
```

---

### Task 2: Dry-run test server (Go httptest)

**Files:**
- Create: `k6/lib/testserver.go`

- [ ] **Step 1: Write the test server**

```go
//go:build ignore

// testserver starts a minimal HTTP server matching target-app's /work contract.
// Used by dry-run_test.go to validate k6 scenarios without a cluster.
package main

import (
    "fmt"
    "net/http"
    "os"
    "time"
)

func main() {
    port := "8080"
    if p := os.Getenv("PORT"); p != "" {
        port = p
    }

    mux := http.NewServeMux()
    mux.HandleFunc("/work", func(w http.ResponseWriter, r *http.Request) {
        time.Sleep(5 * time.Millisecond) // simulate work
        w.WriteHeader(http.StatusOK)
        fmt.Fprintf(w, `{"status":"ok"}`)
    })
    mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
        w.WriteHeader(http.StatusOK)
    })

    fmt.Printf("test server listening on :%s\n", port)
    if err := http.ListenAndServe(":"+port, mux); err != nil {
        fmt.Fprintf(os.Stderr, "server error: %v\n", err)
        os.Exit(1)
    }
}
```

- [ ] **Step 2: Commit**

```bash
git add k6/lib/testserver.go
git commit -m "feat(k6): dry-run test server for scenario validation"
```

---

## Phase 2 — Scenarios (each follows identical structure)

### Task 3: Ramp scenario

**Files:**
- Create: `k6/scenarios/ramp.js`

- [ ] **Step 1: Write ramp.js**

```javascript
import http from "k6/http";
import { check, sleep } from "k6";
import { getTargets, workURL } from "../lib/targets.js";

const targets = getTargets();

const RAMP_UP = __ENV.RAMP_UP_DURATION || "5m";
const HOLD = __ENV.RAMP_HOLD_DURATION || "15m";
const RAMP_DOWN = __ENV.RAMP_DOWN_DURATION || "5m";
const PEAK = parseInt(__ENV.RAMP_RPS_PEAK || "200");

export const options = {
  scenarios: {
    ramp: {
      executor: "ramping-arrival-rate",
      startRate: 0,
      timeUnit: "1s",
      preAllocatedVUs: 50,
      maxVUs: 200,
      stages: [
        { target: PEAK, duration: RAMP_UP },
        { target: PEAK, duration: HOLD },
        { target: 0, duration: RAMP_DOWN },
      ],
    },
  },
  thresholds: {
    "http_req_failed": ["rate<0.05"],
    "http_req_duration{url:agentic}": ["p(95)<2000"],
    "http_req_duration{url:hpa}": ["p(95)<2000"],
  },
};

export default function () {
  const resA = http.post(workURL(targets.agentic), null, {
    tags: { url: "agentic" },
  });
  check(resA, { "agentic status 2xx or 503": (r) => r.status === 200 || r.status === 503 });

  const resH = http.post(workURL(targets.hpa), null, {
    tags: { url: "hpa" },
  });
  check(resH, { "hpa status 2xx or 503": (r) => r.status === 200 || r.status === 503 });
}
```

- [ ] **Step 2: Commit**

```bash
git add k6/scenarios/ramp.js
git commit -m "feat(k6): ramp scenario (0 → peak → hold → 0)"
```

---

### Task 4: Steady scenario

**Files:**
- Create: `k6/scenarios/steady.js`

- [ ] **Step 1: Write steady.js**

```javascript
import http from "k6/http";
import { check } from "k6";
import { getTargets, workURL } from "../lib/targets.js";

const targets = getTargets();

const RPS = parseInt(__ENV.STEADY_RPS || "100");
const DURATION = __ENV.STEADY_DURATION || "10m";

export const options = {
  scenarios: {
    steady: {
      executor: "constant-arrival-rate",
      rate: RPS,
      timeUnit: "1s",
      duration: DURATION,
      preAllocatedVUs: 50,
      maxVUs: 200,
    },
  },
  thresholds: {
    "http_req_failed": ["rate<0.05"],
  },
};

export default function () {
  const resA = http.post(workURL(targets.agentic), null, {
    tags: { url: "agentic" },
  });
  check(resA, { "agentic ok": (r) => r.status === 200 || r.status === 503 });

  const resH = http.post(workURL(targets.hpa), null, {
    tags: { url: "hpa" },
  });
  check(resH, { "hpa ok": (r) => r.status === 200 || r.status === 503 });
}
```

- [ ] **Step 2: Commit**

```bash
git add k6/scenarios/steady.js
git commit -m "feat(k6): steady scenario (constant RPS)"
```

---

### Task 5: Spiky scenario

**Files:**
- Create: `k6/scenarios/spiky.js`

- [ ] **Step 1: Write spiky.js**

```javascript
import http from "k6/http";
import { check, sleep } from "k6";
import { getTargets, workURL } from "../lib/targets.js";

const targets = getTargets();

const BASE_RPS = parseInt(__ENV.SPIKE_BASE_RPS || "50");
const PEAK_RPS = parseInt(__ENV.SPIKE_PEAK_RPS || "500");
const INTERVAL = __ENV.SPIKE_INTERVAL || "2m";
const SPIKE_DURATION = __ENV.SPIKE_DURATION || "30s";
const TOTAL_DURATION = __ENV.SPIKY_TOTAL_DURATION || "20m";

export const options = {
  scenarios: {
    base: {
      executor: "constant-arrival-rate",
      rate: BASE_RPS,
      timeUnit: "1s",
      duration: TOTAL_DURATION,
      preAllocatedVUs: 50,
      maxVUs: 100,
      exec: "base_load",
    },
    spikes: {
      executor: "ramping-arrival-rate",
      startRate: BASE_RPS,
      timeUnit: "1s",
      preAllocatedVUs: 100,
      maxVUs: 600,
      stages: generateSpikeStages(),
      exec: "spike_load",
    },
  },
  thresholds: {
    "http_req_failed": ["rate<0.10"],
  },
};

function generateSpikeStages() {
  const stages = [];
  const intervalSec = parseDuration(INTERVAL);
  const spikeSec = parseDuration(SPIKE_DURATION);
  const totalSec = parseDuration(TOTAL_DURATION);
  let elapsed = 0;

  while (elapsed < totalSec) {
    stages.push({ target: BASE_RPS, duration: `${intervalSec}s` });
    elapsed += intervalSec;
    if (elapsed >= totalSec) break;
    stages.push({ target: PEAK_RPS, duration: `${spikeSec}s` });
    elapsed += spikeSec;
  }
  return stages;
}

function parseDuration(d) {
  const m = d.match(/^(\d+)(s|m|h)$/);
  if (!m) return 60;
  const v = parseInt(m[1]);
  if (m[2] === "m") return v * 60;
  if (m[2] === "h") return v * 3600;
  return v;
}

export function base_load() {
  const resA = http.post(workURL(targets.agentic), null, { tags: { url: "agentic" } });
  check(resA, { "agentic ok": (r) => r.status === 200 || r.status === 503 });
  const resH = http.post(workURL(targets.hpa), null, { tags: { url: "hpa" } });
  check(resH, { "hpa ok": (r) => r.status === 200 || r.status === 503 });
}

export function spike_load() {
  const resA = http.post(workURL(targets.agentic), null, { tags: { url: "agentic" } });
  check(resA, { "agentic spike ok": (r) => r.status === 200 || r.status === 503 });
  const resH = http.post(workURL(targets.hpa), null, { tags: { url: "hpa" } });
  check(resH, { "hpa spike ok": (r) => r.status === 200 || r.status === 503 });
}
```

- [ ] **Step 2: Commit**

```bash
git add k6/scenarios/spiky.js
git commit -m "feat(k6): spiky scenario (periodic bursts at intervals)"
```

---

### Task 6: Bursty scenario

**Files:**
- Create: `k6/scenarios/bursty.js`

- [ ] **Step 1: Write bursty.js**

```javascript
import http from "k6/http";
import { check, sleep } from "k6";
import { getTargets, workURL } from "../lib/targets.js";

const targets = getTargets();

const BURST_SIZE = parseInt(__ENV.BURST_SIZE || "50");
const MIN_INTERVAL = parseInt(__ENV.BURST_MIN_INTERVAL || "5");
const MAX_INTERVAL = parseInt(__ENV.BURST_MAX_INTERVAL || "30");
const TOTAL_DURATION = __ENV.BURSTY_TOTAL_DURATION || "15m";

export const options = {
  scenarios: {
    bursty: {
      executor: "per-vu-iterations",
      vus: 1,
      maxDuration: TOTAL_DURATION,
      exec: "burst_loop",
    },
  },
  thresholds: {
    "http_req_failed": ["rate<0.15"],
  },
};

export function burst_loop() {
  // Fire a burst of BURST_SIZE requests as fast as possible.
  for (let i = 0; i < BURST_SIZE; i++) {
    const resA = http.post(workURL(targets.agentic), null, { tags: { url: "agentic" } });
    check(resA, { "agentic burst ok": (r) => r.status === 200 || r.status === 503 });
    const resH = http.post(workURL(targets.hpa), null, { tags: { url: "hpa" } });
    check(resH, { "hpa burst ok": (r) => r.status === 200 || r.status === 503 });
  }

  // Random pause between bursts.
  const pause = MIN_INTERVAL + Math.random() * (MAX_INTERVAL - MIN_INTERVAL);
  sleep(pause);
}
```

- [ ] **Step 2: Commit**

```bash
git add k6/scenarios/bursty.js
git commit -m "feat(k6): bursty scenario (random burst pattern)"
```

---

## Phase 3 — Dry-run validation

### Task 7: Go test invoking k6 --vus=1

**Files:**
- Create: `k6/dry-run_test.go`

- [ ] **Step 1: Write the dry-run test**

```go
//go:build integration

package k6_test

import (
    "context"
    "fmt"
    "net/http"
    "net/http/httptest"
    "os"
    "os/exec"
    "testing"
    "time"

    "github.com/stretchr/testify/require"
)

func startTestServer(t *testing.T) *httptest.Server {
    t.Helper()
    mux := http.NewServeMux()
    mux.HandleFunc("/work", func(w http.ResponseWriter, _ *http.Request) {
        time.Sleep(2 * time.Millisecond)
        w.WriteHeader(http.StatusOK)
        fmt.Fprintf(w, `{"status":"ok"}`)
    })
    srv := httptest.NewServer(mux)
    t.Cleanup(srv.Close)
    return srv
}

func TestK6DryRun_Ramp(t *testing.T) {
    runK6Scenario(t, "scenarios/ramp.js")
}

func TestK6DryRun_Steady(t *testing.T) {
    runK6Scenario(t, "scenarios/steady.js")
}

func TestK6DryRun_Spiky(t *testing.T) {
    runK6Scenario(t, "scenarios/spiky.js")
}

func TestK6DryRun_Bursty(t *testing.T) {
    runK6Scenario(t, "scenarios/bursty.js")
}

func runK6Scenario(t *testing.T, script string) {
    t.Helper()

    srv := startTestServer(t)

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    cmd := exec.CommandContext(ctx, "k6", "run",
        "--vus=1", "--iterations=5", "--no-color",
        script)
    cmd.Env = append(os.Environ(),
        "TARGET_AGENTIC_URL="+srv.URL,
        "TARGET_HPA_URL="+srv.URL,
        "RAMP_UP_DURATION=1s",
        "RAMP_HOLD_DURATION=1s",
        "RAMP_DOWN_DURATION=1s",
        "RAMP_RPS_PEAK=2",
        "STEADY_RPS=2",
        "STEADY_DURATION=3s",
        "SPIKE_BASE_RPS=1",
        "SPIKE_PEAK_RPS=2",
        "SPIKE_INTERVAL=2s",
        "SPIKE_DURATION=1s",
        "SPIKY_TOTAL_DURATION=5s",
        "BURST_SIZE=2",
        "BURST_MIN_INTERVAL=1",
        "BURST_MAX_INTERVAL=2",
        "BURSTY_TOTAL_DURATION=5s",
    )
    cmd.Dir = "."
    out, err := cmd.CombinedOutput()
    if err != nil {
        t.Logf("k6 output:\n%s", string(out))
    }
    require.NoError(t, err, "k6 dry-run of %s failed", script)
}
```

- [ ] **Step 2: Run (requires k6 installed)**

```bash
cd k6 && go test -tags=integration -v -timeout=60s ./...
```

Expected: all four scenarios pass with `--vus=1` and tiny durations.

- [ ] **Step 3: Commit**

```bash
git add k6/
git commit -m "test(k6): dry-run validation of all four scenarios via httptest"
```

---

## Phase 4 — Milestone

### Task 8: Milestone commit

- [ ] **Step 1: Lint check**

```bash
# Verify JS files have no syntax errors (k6 will error on bad JS)
k6 inspect k6/scenarios/ramp.js
k6 inspect k6/scenarios/steady.js
k6 inspect k6/scenarios/spiky.js
k6 inspect k6/scenarios/bursty.js
```

- [ ] **Step 2: Milestone commit**

```bash
git commit --allow-empty -m "milestone: Plan #9 (k6 scenarios) complete

Four load scenarios: ramp (0→peak→hold→0), steady (constant RPS),
spiky (periodic bursts), bursty (random burst pattern).

All scenarios:
- Drive identical requests to both app-agentic and app-hpa
- Configurable via k6 __ENV vars (durations, RPS peaks, intervals)
- Pass check() assertions in --vus=1 dry-run against httptest server
- Use shared targets.js helper for dual-target URL construction
"
```

---

## Plan-specific Definition of Done

- [ ] Each scenario passes `k6 run --vus=1 --iterations=5` against the test server without errors.
- [ ] All scenarios send identical requests to both target URLs (verified by `getTargets()` usage).
- [ ] RPS and duration parameters are configurable via `__ENV` variables with sensible defaults.
- [ ] `k6 inspect` reports no syntax errors on all four scripts.
- [ ] Dry-run test (`go test -tags=integration`) passes all four scenarios.

---

## Notes on what's intentionally deferred

- **Makefile targets** (`make k6-ramp`, etc.) — Plan #11.
- **In-cluster execution** (k6 pointing at real services) — Plan #10's E2E / Plan #11's nightly.
- **k6 cloud or Grafana k6 integration** — out of scope.
- **Result parsing / assertion on Prometheus queries** — Plan #10 and #11's E2E assertions.

---

## Self-Review

**Spec coverage.** Strategy §7.1 Plan 9 scope is covered: four scenarios, `--vus=1` dry-run, env-var configurability, both targets receive identical streams.

**Placeholders.** None. Every JS file is complete; the Go dry-run test has full implementation.

**Type consistency.** `getTargets()` and `workURL()` exported names are consistent across all four scenarios. Env var names match: `RAMP_RPS_PEAK`, `STEADY_RPS`, `SPIKE_BASE_RPS`, `SPIKE_PEAK_RPS`, `BURST_SIZE`, etc.

---

## Execution handoff

Plan complete and saved. Two execution options:

1. **Subagent-Driven (recommended)**
2. **Inline Execution**

Which approach?
