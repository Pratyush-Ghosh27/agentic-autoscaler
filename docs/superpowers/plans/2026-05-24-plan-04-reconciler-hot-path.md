# Plan 04 — Reconciler Hot Path Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the controller's reconcile loop end-to-end as specified in `docs/design.md` §5 — every step from kill-switch pre-check to status update — with deterministic, fully-tested decision logic that survives controller restarts. The reconciler depends on Plan #1 (CRD types, env-var config, reasoning tokens) and Plan #3 (Prometheus + Forecast adapters); the ExplainWorker channel send (step 10) is implemented as an interface here and consumed by Plan #6.

**Architecture:** Two clean layers. The **decision layer** (`internal/decision/`) is a pure function `Compute(input DecisionInput) DecisionOutput` — no I/O, no clock, no Kubernetes types beyond the CRD spec. Every quirk of design §5 step 5-9 (ring buffer median, restart recovery, `step_capped_*` vs `cooldown_holding_*` precedence, hysteresis) is testable from a Go unit test in microseconds. The **orchestration layer** (`internal/controller/agenticautoscaler_controller.go`) is the kubebuilder reconciler — it does HTTP, reads/writes CR + Deployment, lists HPAs, emits Events, and notifies the ExplainWorker. Both adapters from Plan #3 are injected via interfaces so envtest can swap in fakes.

**Tech Stack:** Go 1.23, controller-runtime v0.19, kubebuilder v4 reconciler scaffold, `client-go` `record.EventRecorder`, testify, envtest, ginkgo/v2 + gomega (continuing from Plan #1's harness).

---

## Spec Coverage Map

| Design §5 step | Plan task |
| --- | --- |
| Preamble — `effectiveCooldownUp/Down`, `effectiveMaxStep`, `effectiveForecaster` resolution chain | T3 |
| Step 1a — kill-switch annotation pre-check | T11 |
| Step 1b — CR being deleted | T11 |
| Step 1c — HPA conflict pre-check (list HPAs, match `scaleTargetRef`) | T13 |
| Step 2 — Prometheus instant query + range query (history) | T9, T12 |
| Step 2 — `HOT_PATH_MIN_POINTS` floor → `metrics_unavailable` no-op | T14 |
| Step 3 — POST `/recommend` with `effectiveForecaster` (omit "auto") | T12 |
| Step 4 — receive response, validate via Plan #3 adapter | T12 |
| Step 5 — restart recovery (seed `rpsPerPod`, observations, `lastScaleTime`) | T8 |
| Step 5 — steady-state gate (skip update if scale within 2× interval) | T7 |
| Step 5 — ring buffer median + clamp | T6 |
| Step 5 — `recommendedReplicas` = clamp(ceil(predicted/rps_per_pod), min, max) | T4 |
| Step 6 — `maxStepSize` cap; tentative `step_capped_*` reasoning | T5 |
| Step 7 — cooldowns; `cooldown_holding_*` overrides `step_capped_*` when no patch | T5 |
| Step 8 — hysteresis (no patch when `target == current`) | T5 |
| Step 9 — patch `/scale` subresource | T15 |
| Step 10 — emit K8s Event with reasoning token; drop-and-replace ExplainWorker send | T15, T16 |
| Step 11 — update CR status (`recommendedReplicas` is pre-cap; `lastScaleTime`) | T15 |
| §9 — Prometheus error → `metrics_unavailable` | T14 |
| §9 — Forecast error → `forecast_unavailable`, no replica change | T14 |
| §9 — `/scale` patch error → leave status, retry | T14 |
| §9 — cold-start (no `classifiedParams` yet) → env-var defaults via nil-coalesce | T3 |

What's intentionally not in this plan: the ClassifierWorker that writes `status.classifiedParams` (Plan #5), the ExplainWorker that consumes the channel (Plan #6), and the manifests / RBAC roles that get layered on top (Plan #9). The reconciler **reads** `classifiedParams` but never writes it (line 399 of design.md is explicit about this).

---

## File Structure

```
scaler/
├── api/v1alpha1/                                 # touched: only RBAC markers (no schema changes)
├── internal/
│   ├── decision/                                 # pure decision layer
│   │   ├── decision.go                           # T3-T8: Compute() + helpers
│   │   ├── decision_test.go                      # T3-T8: full table-driven coverage
│   │   ├── state.go                              # T8: PerCRState + StateStore (ring buffer)
│   │   └── state_test.go                         # T8
│   ├── promql/
│   │   ├── builder.go                            # T9: PromQL string construction
│   │   └── builder_test.go                       # T9
│   ├── controller/
│   │   ├── agenticautoscaler_controller.go       # T1, T11-T16: orchestration
│   │   ├── agenticautoscaler_controller_test.go  # T11-T16: envtest specs
│   │   ├── interfaces.go                         # T10: PromQuerier, Forecaster, ExplainNotifier
│   │   └── fakes_test.go                         # T11+: in-memory fakes for envtest
│   └── reasoning/                                 # extended (Plan #1 ships base; we add tokens not yet present)
│       └── tokens.go
├── cmd/controller/main.go                        # T2: register reconciler with manager
└── config/rbac/                                   # T2: kubebuilder generates from markers
    └── role.yaml
```

### File responsibilities

- `internal/decision/decision.go` — `Compute(DecisionInput) DecisionOutput` is a pure function. No imports of `client-go`, `controller-runtime`, or `time.Now()` (clock is injected). Tested exhaustively in microseconds.
- `internal/decision/state.go` — `PerCRState{RingBuffer, LastScaleUpTime, LastScaleDownTime}` plus `StateStore` (a sync-protected `map[NamespacedName]*PerCRState`). Both have tests.
- `internal/promql/builder.go` — given a `targetRef` and config, builds the instant + range query strings. Pure function; trivially tested.
- `internal/controller/interfaces.go` — three small interfaces (`PromQuerier`, `Forecaster`, `ExplainNotifier`) so envtest can substitute fakes. The real `prometheus.Client` and `forecast.Client` from Plan #3 satisfy the first two; an in-process channel implements the third.
- `internal/controller/agenticautoscaler_controller.go` — the kubebuilder `AgenticAutoscalerReconciler`. Composition: holds the three interfaces, the `StateStore`, the `EventRecorder`, the kube `client.Client`, and the `Config` from Plan #1.
- `internal/controller/fakes_test.go` — `fakePromQuerier`, `fakeForecaster`, `fakeExplainNotifier`. They record calls and return scripted responses. Used by every envtest spec in this plan.

---

## Phase 0 — Scaffolding

### Task 1: Generate the kubebuilder controller scaffold

**Files:**
- Modify: `internal/controller/agenticautoscaler_controller.go`
- Modify: `cmd/controller/main.go`
- Modify: `config/rbac/role.yaml`

- [ ] **Step 1: Run kubebuilder create controller (if not already scaffolded from Plan #1's --controller=false)**

If Plan #1 used `--controller=false`, the controller file doesn't exist yet:

```bash
kubebuilder create api \
  --group autoscaling \
  --version v1alpha1 \
  --kind AgenticAutoscaler \
  --controller=true \
  --resource=false
```

If the controller file already exists from Plan #1 (unlikely given `--controller=false`), skip this step.

- [ ] **Step 2: Verify compilation**

```bash
go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add .
git commit -m "feat(controller): scaffold AgenticAutoscaler reconciler"
```

---

### Task 2: Wire reconciler dependencies into cmd/controller/main.go

**Files:**
- Modify: `cmd/controller/main.go`
- Modify: `internal/controller/agenticautoscaler_controller.go`

- [ ] **Step 1: Extend the reconciler struct**

In `internal/controller/agenticautoscaler_controller.go`, replace the generated `AgenticAutoscalerReconciler` with the full struct skeleton:

```go
type AgenticAutoscalerReconciler struct {
    client.Client
    Scheme         *runtime.Scheme
    EventRecorder  record.EventRecorder
    Config         *config.Config
    PromQuerier    PromQuerier
    Forecaster     Forecaster
    ExplainNotify  ExplainNotifier
    StateStore     *decision.StateStore
}
```

Add necessary imports. The `Reconcile` body stays as a TODO placeholder for now (just log + return).

- [ ] **Step 2: Inject dependencies in main.go**

After `config.LoadFromEnv()`, create the Prometheus + Forecast clients (Plan #3) and construct the reconciler:

```go
promClient := prometheus.New(cfg.PrometheusURL, time.Duration(cfg.ForecastTimeoutSeconds)*time.Second)
forecastClient := forecast.New(cfg.ForecastServiceURL, time.Duration(cfg.ForecastTimeoutSeconds)*time.Second)
explainCh := make(chan decision.ExplainRequest, 1)

rec := &controller.AgenticAutoscalerReconciler{
    Client:        mgr.GetClient(),
    Scheme:        mgr.GetScheme(),
    EventRecorder: mgr.GetEventRecorderFor("agenticautoscaler-controller"),
    Config:        cfg,
    PromQuerier:   promClient,
    Forecaster:    forecastClient,
    ExplainNotify: controller.ChannelNotifier{Ch: explainCh},
    StateStore:    decision.NewStateStore(),
}
```

Register via `rec.SetupWithManager(mgr)`.

- [ ] **Step 3: Verify compilation**

```bash
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add .
git commit -m "feat(controller): wire reconciler struct with adapter injection"
```

---

## Phase 1 — Decision layer (Tier-1 strict TDD)

This is the highest-value testing target in the entire controller. Every non-trivial number produced by the reconciler originates here.

### Task 3: Effective parameter resolution (nil-coalesce chain)

**Files:**
- Create: `internal/decision/decision.go`
- Create: `internal/decision/decision_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/decision/decision_test.go`:

```go
package decision_test

import (
    "testing"

    "github.com/stretchr/testify/assert"

    "github.com/pratyush-ghosh/agentic-autoscaler/internal/decision"
)

func ptr32(v int32) *int32 { return &v }
func ptrStr(s string) *string { return &s }

func TestResolveEffectiveParams_SpecOverridesAll(t *testing.T) {
    p := decision.ResolveEffectiveParams(decision.ParamSources{
        Spec: decision.SpecParams{
            ScaleUpCooldown:     ptr32(30),
            ScaleDownCooldown:   ptr32(90),
            MaxStepSize:         ptr32(2),
            PreferredForecaster: ptrStr("prophet"),
        },
        Classified: &decision.ClassifiedParams{
            ScaleUpCooldown:     60,
            ScaleDownCooldown:   300,
            MaxStepSize:         3,
            PreferredForecaster: "linear_extrap",
        },
        Defaults: decision.DefaultParams{
            ScaleUpCooldown:   60,
            ScaleDownCooldown: 300,
            MaxStepSize:       4,
        },
    })
    assert.Equal(t, int32(30), p.CooldownUp)
    assert.Equal(t, int32(90), p.CooldownDown)
    assert.Equal(t, int32(2), p.MaxStep)
    assert.Equal(t, "prophet", p.Forecaster)
}

func TestResolveEffectiveParams_ClassifiedFallback(t *testing.T) {
    p := decision.ResolveEffectiveParams(decision.ParamSources{
        Spec: decision.SpecParams{}, // all nil
        Classified: &decision.ClassifiedParams{
            ScaleUpCooldown:     120,
            ScaleDownCooldown:   180,
            MaxStepSize:         1,
            PreferredForecaster: "prophet",
        },
        Defaults: decision.DefaultParams{
            ScaleUpCooldown:   60,
            ScaleDownCooldown: 300,
            MaxStepSize:       4,
        },
    })
    assert.Equal(t, int32(120), p.CooldownUp)
    assert.Equal(t, int32(180), p.CooldownDown)
    assert.Equal(t, int32(1), p.MaxStep)
    assert.Equal(t, "prophet", p.Forecaster)
}

func TestResolveEffectiveParams_DefaultsFallback(t *testing.T) {
    p := decision.ResolveEffectiveParams(decision.ParamSources{
        Spec:       decision.SpecParams{},
        Classified: nil, // cold start
        Defaults: decision.DefaultParams{
            ScaleUpCooldown:   60,
            ScaleDownCooldown: 300,
            MaxStepSize:       4,
        },
    })
    assert.Equal(t, int32(60), p.CooldownUp)
    assert.Equal(t, int32(300), p.CooldownDown)
    assert.Equal(t, int32(4), p.MaxStep)
    assert.Equal(t, "auto", p.Forecaster, "'auto' when nothing overrides")
}
```

- [ ] **Step 2: Run; expect ImportError**

- [ ] **Step 3: Implement**

Create `internal/decision/decision.go`:

```go
// Package decision implements the pure scaling decision logic for the
// AgenticAutoscaler controller. It has zero I/O, zero Kubernetes imports
// beyond the CRD spec shapes, and zero calls to time.Now (clock is injected).
package decision

// ParamSources feeds the nil-coalesce resolution chain (design ss5 preamble).
type ParamSources struct {
    Spec       SpecParams
    Classified *ClassifiedParams // nil when cold-start (no classifier run yet)
    Defaults   DefaultParams
}

type SpecParams struct {
    ScaleUpCooldown     *int32
    ScaleDownCooldown   *int32
    MaxStepSize         *int32
    PreferredForecaster *string
}

type ClassifiedParams struct {
    ScaleUpCooldown     int32
    ScaleDownCooldown   int32
    MaxStepSize         int32
    PreferredForecaster string
}

type DefaultParams struct {
    ScaleUpCooldown   int32
    ScaleDownCooldown int32
    MaxStepSize       int32
}

// EffectiveParams is the resolved output of the nil-coalesce chain.
type EffectiveParams struct {
    CooldownUp   int32
    CooldownDown int32
    MaxStep      int32
    Forecaster   string // "prophet", "linear_extrap", or "auto"
}

// ResolveEffectiveParams applies spec ?? classified ?? defaults for each field.
func ResolveEffectiveParams(src ParamSources) EffectiveParams {
    return EffectiveParams{
        CooldownUp:   coalesce32(src.Spec.ScaleUpCooldown, classifiedOrNil32(src.Classified, func(c *ClassifiedParams) int32 { return c.ScaleUpCooldown }), src.Defaults.ScaleUpCooldown),
        CooldownDown: coalesce32(src.Spec.ScaleDownCooldown, classifiedOrNil32(src.Classified, func(c *ClassifiedParams) int32 { return c.ScaleDownCooldown }), src.Defaults.ScaleDownCooldown),
        MaxStep:      coalesce32(src.Spec.MaxStepSize, classifiedOrNil32(src.Classified, func(c *ClassifiedParams) int32 { return c.MaxStepSize }), src.Defaults.MaxStepSize),
        Forecaster:   coalesceStr(src.Spec.PreferredForecaster, classifiedOrNilStr(src.Classified, func(c *ClassifiedParams) string { return c.PreferredForecaster }), "auto"),
    }
}

func coalesce32(spec *int32, classified *int32, def int32) int32 {
    if spec != nil { return *spec }
    if classified != nil { return *classified }
    return def
}

func classifiedOrNil32(c *ClassifiedParams, f func(*ClassifiedParams) int32) *int32 {
    if c == nil { return nil }
    v := f(c)
    return &v
}

func coalesceStr(spec *string, classified *string, def string) string {
    if spec != nil && *spec != "" { return *spec }
    if classified != nil && *classified != "" { return *classified }
    return def
}

func classifiedOrNilStr(c *ClassifiedParams, f func(*ClassifiedParams) string) *string {
    if c == nil { return nil }
    v := f(c)
    if v == "" { return nil }
    return &v
}
```

- [ ] **Step 4: Run; verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/decision/
git commit -m "feat(decision): nil-coalesce parameter resolution chain"
```

---

### Task 4: recommendedReplicas = clamp(ceil(predicted/rps_per_pod), min, max)

**Files:**
- Modify: `internal/decision/decision.go`
- Modify: `internal/decision/decision_test.go`

- [ ] **Step 1: Append failing tests**

```go
func TestComputeRecommended_Basic(t *testing.T) {
    cases := []struct {
        name string
        predicted float64
        rpsPerPod float64
        min, max  int32
        want      int32
    }{
        {"exact fit", 300, 100, 1, 10, 3},
        {"fractional ceil", 301, 100, 1, 10, 4},
        {"clamp to min", 50, 100, 2, 10, 2},
        {"clamp to max", 2000, 100, 1, 5, 5},
        {"zero predicted", 0, 100, 1, 10, 1},
        {"very low rps_per_pod", 100, 1, 1, 100, 100},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got := decision.ComputeRecommended(tc.predicted, tc.rpsPerPod, tc.min, tc.max)
            assert.Equal(t, tc.want, got)
        })
    }
}
```

- [ ] **Step 2: Run; expect failure**

- [ ] **Step 3: Implement**

```go
import "math"

// ComputeRecommended calculates the raw recommendedReplicas (pre-cap, pre-cooldown).
func ComputeRecommended(predictedRPS, rpsPerPod float64, minReplicas, maxReplicas int32) int32 {
    if rpsPerPod <= 0 {
        return maxReplicas
    }
    raw := int32(math.Ceil(predictedRPS / rpsPerPod))
    if raw < minReplicas { return minReplicas }
    if raw > maxReplicas { return maxReplicas }
    return raw
}
```

- [ ] **Step 4: Run; verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/decision/
git commit -m "feat(decision): ComputeRecommended (clamp + ceil)"
```

---

### Task 5: ApplyCapAndCooldown — step size cap, cooldowns, hysteresis, reasoning tokens

**Files:**
- Modify: `internal/decision/decision.go`
- Modify: `internal/decision/decision_test.go`

This is the most nuanced logic: the interplay between `maxStepSize`, cooldowns, and the `step_capped_*` / `cooldown_holding_*` token precedence rule (design ss5 step 6-8).

- [ ] **Step 1: Append failing tests**

```go
import "time"

func TestApplyCapAndCooldown(t *testing.T) {
    now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
    longAgo := now.Add(-1 * time.Hour)

    cases := []struct {
        name        string
        in          decision.CapInput
        wantTarget  int32
        wantReason  string
        wantPatched bool
    }{
        {
            name: "scale up within cap and cooldown",
            in: decision.CapInput{
                Recommended: 6, Current: 4, MaxStep: 4,
                CooldownUp: 60, CooldownDown: 300,
                LastScaleUp: longAgo, LastScaleDown: longAgo, Now: now,
            },
            wantTarget: 6, wantReason: "scale_up", wantPatched: true,
        },
        {
            name: "scale up capped by maxStepSize",
            in: decision.CapInput{
                Recommended: 10, Current: 4, MaxStep: 2,
                CooldownUp: 60, CooldownDown: 300,
                LastScaleUp: longAgo, LastScaleDown: longAgo, Now: now,
            },
            wantTarget: 6, wantReason: "step_capped_up", wantPatched: true,
        },
        {
            name: "scale up blocked by cooldown",
            in: decision.CapInput{
                Recommended: 6, Current: 4, MaxStep: 4,
                CooldownUp: 60, CooldownDown: 300,
                LastScaleUp: now.Add(-30 * time.Second), LastScaleDown: longAgo, Now: now,
            },
            wantTarget: 4, wantReason: "cooldown_holding_up", wantPatched: false,
        },
        {
            name: "cap + cooldown: cooldown wins (no patch)",
            in: decision.CapInput{
                Recommended: 10, Current: 4, MaxStep: 2,
                CooldownUp: 60, CooldownDown: 300,
                LastScaleUp: now.Add(-30 * time.Second), LastScaleDown: longAgo, Now: now,
            },
            wantTarget: 4, wantReason: "cooldown_holding_up", wantPatched: false,
        },
        {
            name: "scale down within cap and cooldown",
            in: decision.CapInput{
                Recommended: 3, Current: 5, MaxStep: 4,
                CooldownUp: 60, CooldownDown: 300,
                LastScaleUp: longAgo, LastScaleDown: longAgo, Now: now,
            },
            wantTarget: 3, wantReason: "scale_down", wantPatched: true,
        },
        {
            name: "scale down capped",
            in: decision.CapInput{
                Recommended: 1, Current: 5, MaxStep: 2,
                CooldownUp: 60, CooldownDown: 300,
                LastScaleUp: longAgo, LastScaleDown: longAgo, Now: now,
            },
            wantTarget: 3, wantReason: "step_capped_down", wantPatched: true,
        },
        {
            name: "scale down blocked by cooldown",
            in: decision.CapInput{
                Recommended: 3, Current: 5, MaxStep: 4,
                CooldownUp: 60, CooldownDown: 300,
                LastScaleUp: longAgo, LastScaleDown: now.Add(-100 * time.Second), Now: now,
            },
            wantTarget: 5, wantReason: "cooldown_holding_down", wantPatched: false,
        },
        {
            name: "no change (hysteresis)",
            in: decision.CapInput{
                Recommended: 5, Current: 5, MaxStep: 4,
                CooldownUp: 60, CooldownDown: 300,
                LastScaleUp: longAgo, LastScaleDown: longAgo, Now: now,
            },
            wantTarget: 5, wantReason: "no_change", wantPatched: false,
        },
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            out := decision.ApplyCapAndCooldown(tc.in)
            assert.Equal(t, tc.wantTarget, out.Target, "target")
            assert.Equal(t, tc.wantReason, out.Reason, "reasoning token")
            assert.Equal(t, tc.wantPatched, out.ShouldPatch, "should patch")
        })
    }
}
```

- [ ] **Step 2: Run; expect failure**

- [ ] **Step 3: Implement**

Append to `internal/decision/decision.go`:

```go
import "time"

// CapInput feeds ApplyCapAndCooldown.
type CapInput struct {
    Recommended   int32
    Current       int32
    MaxStep       int32
    CooldownUp    int32 // seconds
    CooldownDown  int32 // seconds
    LastScaleUp   time.Time
    LastScaleDown time.Time
    Now           time.Time
}

// CapOutput is the result of applying step cap + cooldown + hysteresis.
type CapOutput struct {
    Target      int32
    Reason      string
    ShouldPatch bool
}

// ApplyCapAndCooldown implements design ss5 steps 6-8.
func ApplyCapAndCooldown(in CapInput) CapOutput {
    target := in.Recommended
    reason := ""

    switch {
    case target > in.Current:
        // Step 6: cap.
        if target > in.Current+in.MaxStep {
            target = in.Current + in.MaxStep
            reason = "step_capped_up"
        } else {
            reason = "scale_up"
        }
        // Step 7: cooldown overrides.
        if in.Now.Sub(in.LastScaleUp) < time.Duration(in.CooldownUp)*time.Second {
            target = in.Current
            reason = "cooldown_holding_up"
        }
    case target < in.Current:
        if target < in.Current-in.MaxStep {
            target = in.Current - in.MaxStep
            reason = "step_capped_down"
        } else {
            reason = "scale_down"
        }
        if in.Now.Sub(in.LastScaleDown) < time.Duration(in.CooldownDown)*time.Second {
            target = in.Current
            reason = "cooldown_holding_down"
        }
    default:
        target = in.Current
        reason = "no_change"
    }

    return CapOutput{
        Target:      target,
        Reason:      reason,
        ShouldPatch: target != in.Current,
    }
}
```

- [ ] **Step 4: Run; verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/decision/
git commit -m "feat(decision): ApplyCapAndCooldown (step cap, cooldowns, hysteresis)"
```

---

### Task 6: Ring buffer + median for rps_per_pod

**Files:**
- Create: `internal/decision/state.go`
- Create: `internal/decision/state_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/decision/state_test.go`:

```go
package decision_test

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"

    "github.com/pratyush-ghosh/agentic-autoscaler/internal/decision"
)

func TestRingBuffer_MedianOddCount(t *testing.T) {
    rb := decision.NewRingBuffer(10)
    rb.Push(100)
    rb.Push(200)
    rb.Push(150)
    assert.InDelta(t, 150.0, rb.Median(), 0.001)
}

func TestRingBuffer_MedianEvenCount(t *testing.T) {
    rb := decision.NewRingBuffer(10)
    rb.Push(100)
    rb.Push(200)
    assert.InDelta(t, 150.0, rb.Median(), 0.001)
}

func TestRingBuffer_EvictsOldest(t *testing.T) {
    rb := decision.NewRingBuffer(3)
    rb.Push(100)
    rb.Push(200)
    rb.Push(300)
    rb.Push(400) // evicts 100
    assert.Len(t, rb.Values(), 3)
    assert.InDelta(t, 300.0, rb.Median(), 0.001) // median of [200, 300, 400]
}

func TestRingBuffer_EmptyReturnsZero(t *testing.T) {
    rb := decision.NewRingBuffer(10)
    assert.Equal(t, 0.0, rb.Median())
}

func TestRingBuffer_SingleElement(t *testing.T) {
    rb := decision.NewRingBuffer(10)
    rb.Push(42)
    assert.InDelta(t, 42.0, rb.Median(), 0.001)
}

func TestRingBuffer_SeedWithOne(t *testing.T) {
    rb := decision.NewRingBuffer(10)
    rb.Seed(175)
    require.Len(t, rb.Values(), 1)
    assert.InDelta(t, 175.0, rb.Median(), 0.001)
}
```

- [ ] **Step 2: Run; expect failure**

- [ ] **Step 3: Implement**

Create `internal/decision/state.go`:

```go
package decision

import (
    "sort"
    "sync"
    "time"

    "k8s.io/apimachinery/pkg/types"
)

// RingBuffer holds up to `cap` float64 observations in FIFO order.
type RingBuffer struct {
    data []float64
    cap  int
}

func NewRingBuffer(capacity int) *RingBuffer {
    return &RingBuffer{data: make([]float64, 0, capacity), cap: capacity}
}

func (rb *RingBuffer) Push(v float64) {
    if len(rb.data) == rb.cap {
        rb.data = rb.data[1:]
    }
    rb.data = append(rb.data, v)
}

func (rb *RingBuffer) Seed(v float64) {
    rb.data = append(rb.data[:0], v)
}

func (rb *RingBuffer) Values() []float64 { return rb.data }

func (rb *RingBuffer) Median() float64 {
    n := len(rb.data)
    if n == 0 { return 0 }
    sorted := make([]float64, n)
    copy(sorted, rb.data)
    sort.Float64s(sorted)
    if n%2 == 1 {
        return sorted[n/2]
    }
    return (sorted[n/2-1] + sorted[n/2]) / 2
}

// PerCRState holds the in-memory state for one AgenticAutoscaler CR.
type PerCRState struct {
    Observations    *RingBuffer
    RpsPerPod       float64
    LastScaleUpTime   time.Time
    LastScaleDownTime time.Time
    Initialized     bool
}

// StateStore is a concurrency-safe map of per-CR states.
type StateStore struct {
    mu    sync.RWMutex
    store map[types.NamespacedName]*PerCRState
}

func NewStateStore() *StateStore {
    return &StateStore{store: make(map[types.NamespacedName]*PerCRState)}
}

func (s *StateStore) Get(key types.NamespacedName) *PerCRState {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.store[key]
}

func (s *StateStore) GetOrCreate(key types.NamespacedName, capacity int) *PerCRState {
    s.mu.Lock()
    defer s.mu.Unlock()
    if st, ok := s.store[key]; ok {
        return st
    }
    st := &PerCRState{Observations: NewRingBuffer(capacity)}
    s.store[key] = st
    return st
}
```

- [ ] **Step 4: Run; verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/decision/
git commit -m "feat(decision): RingBuffer + PerCRState + StateStore"
```

---

### Task 7: Steady-state gate

**Files:**
- Modify: `internal/decision/decision.go`
- Modify: `internal/decision/decision_test.go`

- [ ] **Step 1: Append failing tests**

```go
func TestShouldUpdateRpsPerPod(t *testing.T) {
    now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
    interval := 60 * time.Second

    cases := []struct {
        name       string
        currentRPS float64
        replicas   int32
        lastScale  time.Time
        want       bool
    }{
        {"steady state", 1000, 5, now.Add(-5 * time.Minute), true},
        {"recently scaled", 1000, 5, now.Add(-90 * time.Second), false},
        {"low RPS", 5, 5, now.Add(-5 * time.Minute), false},
        {"zero replicas", 1000, 0, now.Add(-5 * time.Minute), false},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got := decision.ShouldUpdateRpsPerPod(tc.currentRPS, tc.replicas, tc.lastScale, now, interval)
            assert.Equal(t, tc.want, got)
        })
    }
}
```

- [ ] **Step 2: Run; expect failure**

- [ ] **Step 3: Implement**

```go
// ShouldUpdateRpsPerPod implements the steady-state gate (design ss5 step 5).
// Skip if current_rps < 10 OR replicas < 1 OR last scale within 2x interval.
func ShouldUpdateRpsPerPod(currentRPS float64, replicas int32, lastScale, now time.Time, interval time.Duration) bool {
    if currentRPS < 10 || replicas < 1 {
        return false
    }
    return now.Sub(lastScale) >= 2*interval
}
```

- [ ] **Step 4: Run; verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/decision/
git commit -m "feat(decision): steady-state gate for rps_per_pod update"
```

---

### Task 8: Restart recovery + ClampRpsPerPod

**Files:**
- Modify: `internal/decision/decision.go`
- Modify: `internal/decision/decision_test.go`

- [ ] **Step 1: Append failing tests**

```go
func TestClampRpsPerPod(t *testing.T) {
    assert.InDelta(t, 50.0, decision.ClampRpsPerPod(30, 50, 500), 0.001)
    assert.InDelta(t, 500.0, decision.ClampRpsPerPod(600, 50, 500), 0.001)
    assert.InDelta(t, 200.0, decision.ClampRpsPerPod(200, 50, 500), 0.001)
}

func TestInitializeState_FromStatus(t *testing.T) {
    state := &decision.PerCRState{Observations: decision.NewRingBuffer(10)}
    decision.InitializeFromStatus(state, decision.StatusSeed{
        RpsPerPodCurrent: 175,
        InBounds:         true,
        LastScaleTime:    time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC),
    })
    assert.True(t, state.Initialized)
    assert.InDelta(t, 175.0, state.RpsPerPod, 0.001)
    assert.Len(t, state.Observations.Values(), 1)
    assert.Equal(t, state.LastScaleUpTime, time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC))
    assert.Equal(t, state.LastScaleDownTime, time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC))
}

func TestInitializeState_Midpoint(t *testing.T) {
    state := &decision.PerCRState{Observations: decision.NewRingBuffer(10)}
    decision.InitializeFromStatus(state, decision.StatusSeed{
        RpsPerPodCurrent: 0, // absent or out-of-bounds
        InBounds:         false,
        Midpoint:         275,
    })
    assert.True(t, state.Initialized)
    assert.InDelta(t, 275.0, state.RpsPerPod, 0.001)
    assert.Empty(t, state.Observations.Values(), "no seeding when OOB")
}
```

- [ ] **Step 2: Run; expect failure**

- [ ] **Step 3: Implement**

```go
// ClampRpsPerPod clamps rps_per_pod within [min, max].
func ClampRpsPerPod(v float64, min, max int32) float64 {
    if v < float64(min) { return float64(min) }
    if v > float64(max) { return float64(max) }
    return v
}

// StatusSeed holds values read from the CR status for restart recovery.
type StatusSeed struct {
    RpsPerPodCurrent float64
    InBounds         bool // true if rpsPerPodCurrent is within [rpsPerPodMin, rpsPerPodMax]
    Midpoint         float64 // (rpsPerPodMin + rpsPerPodMax) / 2
    LastScaleTime    time.Time
}

// InitializeFromStatus seeds a PerCRState from the CR's persisted status
// (design ss5 step 5 "first-time-seeing-this-CR initialisation").
func InitializeFromStatus(state *PerCRState, seed StatusSeed) {
    if seed.InBounds && seed.RpsPerPodCurrent > 0 {
        state.RpsPerPod = seed.RpsPerPodCurrent
        state.Observations.Seed(seed.RpsPerPodCurrent)
    } else {
        state.RpsPerPod = seed.Midpoint
    }
    if !seed.LastScaleTime.IsZero() {
        state.LastScaleUpTime = seed.LastScaleTime
        state.LastScaleDownTime = seed.LastScaleTime
    }
    state.Initialized = true
}
```

- [ ] **Step 4: Run; verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/decision/
git commit -m "feat(decision): restart recovery + ClampRpsPerPod"
```

---

## Phase 2 — PromQL builder + interfaces

### Task 9: PromQL string builder

**Files:**
- Create: `internal/promql/builder.go`
- Create: `internal/promql/builder_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/promql/builder_test.go`:

```go
package promql_test

import (
    "testing"

    "github.com/stretchr/testify/assert"

    "github.com/pratyush-ghosh/agentic-autoscaler/internal/promql"
)

func TestInstantRPSQuery(t *testing.T) {
    got := promql.InstantRPS("demo")
    assert.Equal(t, `sum(rate(http_requests_total{deployment="demo"}[2m]))`, got)
}

func TestInstantRPSQuery_SpecialChars(t *testing.T) {
    got := promql.InstantRPS("my-app-v2")
    assert.Equal(t, `sum(rate(http_requests_total{deployment="my-app-v2"}[2m]))`, got)
}

func TestRangeRPSQuery(t *testing.T) {
    got := promql.RangeRPS("demo")
    assert.Equal(t, `sum(rate(http_requests_total{deployment="demo"}[2m]))`, got,
        "range query uses same expression; start/end/step are URL params handled by the adapter")
}
```

- [ ] **Step 2: Run; expect failure**

- [ ] **Step 3: Implement**

Create `internal/promql/builder.go`:

```go
// Package promql constructs PromQL strings for the controller.
package promql

import "fmt"

// InstantRPS returns the PromQL for the hot-path instant query (design ss5 step 2).
func InstantRPS(deploymentName string) string {
    return fmt.Sprintf(`sum(rate(http_requests_total{deployment="%s"}[2m]))`, deploymentName)
}

// RangeRPS returns the PromQL for the range query. The expression is identical
// to InstantRPS; start/end/step are URL parameters managed by the Prometheus
// adapter (Plan #3).
func RangeRPS(deploymentName string) string {
    return InstantRPS(deploymentName)
}
```

- [ ] **Step 4: Run; verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/promql/
git commit -m "feat(promql): PromQL string builder for instant + range queries"
```

---

### Task 10: Interfaces for adapter injection

**Files:**
- Create: `internal/controller/interfaces.go`

- [ ] **Step 1: Define the three interfaces**

Create `internal/controller/interfaces.go`:

```go
package controller

import (
    "context"
    "time"

    "github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/forecast"
    "github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/prometheus"
)

// PromQuerier is satisfied by prometheus.Client (Plan #3).
type PromQuerier interface {
    InstantQuery(ctx context.Context, query string) (float64, error)
    RangeQuery(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]prometheus.Sample, error)
}

// Forecaster is satisfied by forecast.Client (Plan #3).
type Forecaster interface {
    Recommend(ctx context.Context, req forecast.RecommendRequest) (forecast.RecommendResponse, error)
}

// ExplainNotifier notifies the ExplainWorker of a scaling event via a buffered
// channel with drop-and-replace semantics. Satisfied by ChannelNotifier.
type ExplainNotifier interface {
    Notify(req ExplainRequest)
}

// ExplainRequest carries the context for a scale-explanation LLM call.
type ExplainRequest struct {
    Namespace       string
    Name            string
    Reason          string
    CurrentReplicas int32
    TargetReplicas  int32
    CurrentRPS      float64
    PredictedRPS    float64
    ModelUsed       string
}

// ChannelNotifier implements ExplainNotifier with drop-and-replace semantics.
type ChannelNotifier struct {
    Ch chan ExplainRequest
}

// Notify implements ExplainNotifier. If the channel already has an event queued,
// it drains the stale one and replaces it with the new one (design ss6.2).
func (cn ChannelNotifier) Notify(req ExplainRequest) {
    select {
    case <-cn.Ch: // drain stale
    default:
    }
    select {
    case cn.Ch <- req:
    default:
    }
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add internal/controller/interfaces.go
git commit -m "feat(controller): define PromQuerier, Forecaster, ExplainNotifier interfaces"
```

---

## Phase 3 — Orchestration layer (Tier-2 envtest)

### Task 11: Pre-checks (kill-switch, deletion)

**Files:**
- Modify: `internal/controller/agenticautoscaler_controller.go`
- Create: `internal/controller/fakes_test.go`
- Modify: `internal/controller/agenticautoscaler_controller_test.go` (or webhook_test.go)

- [ ] **Step 1: Implement fakes**

Create `internal/controller/fakes_test.go`:

```go
package controller_test

import (
    "context"
    "time"

    "github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/forecast"
    "github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/prometheus"
    controller "github.com/pratyush-ghosh/agentic-autoscaler/internal/controller"
)

type fakePromQuerier struct {
    instantVal float64
    instantErr error
    rangeVal   []prometheus.Sample
    rangeErr   error
}

func (f *fakePromQuerier) InstantQuery(_ context.Context, _ string) (float64, error) {
    return f.instantVal, f.instantErr
}

func (f *fakePromQuerier) RangeQuery(_ context.Context, _ string, _, _ time.Time, _ time.Duration) ([]prometheus.Sample, error) {
    return f.rangeVal, f.rangeErr
}

type fakeForecaster struct {
    resp forecast.RecommendResponse
    err  error
}

func (f *fakeForecaster) Recommend(_ context.Context, _ forecast.RecommendRequest) (forecast.RecommendResponse, error) {
    return f.resp, f.err
}

type fakeExplainNotifier struct {
    lastReq *controller.ExplainRequest
}

func (f *fakeExplainNotifier) Notify(req controller.ExplainRequest) {
    f.lastReq = &req
}
```

- [ ] **Step 2: Implement the kill-switch + deletion pre-check in Reconcile**

In `internal/controller/agenticautoscaler_controller.go`, fill in the `Reconcile` method. Start with:

```go
func (r *AgenticAutoscalerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := ctrl.LoggerFrom(ctx)

    var aas autoscalingv1alpha1.AgenticAutoscaler
    if err := r.Get(ctx, req.NamespacedName, &aas); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // Pre-check 1a: kill switch.
    if aas.Annotations[reasoning.AnnotationKillSwitch] == "true" {
        aas.Status.Phase = "Disabled"
        if err := r.Status().Update(ctx, &aas); err != nil {
            return ctrl.Result{}, err
        }
        r.EventRecorder.Event(&aas, corev1.EventTypeWarning, "KillSwitched", reasoning.KillSwitched)
        return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
    }

    // Pre-check 1b: deletion — handled by IgnoreNotFound above + finalizer-free design.

    // ... remaining steps below in subsequent tasks ...
    return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
}
```

- [ ] **Step 3: Write the envtest spec**

Add to `internal/controller/agenticautoscaler_controller_test.go`:

```go
var _ = Describe("Reconciler pre-checks", func() {
    It("sets phase Disabled when kill-switch annotation is true", func() {
        cr := newValidCR("killswitch-test")
        cr.Annotations = map[string]string{
            "autoscaling.agentic.io/kill-switch": "true",
        }
        Expect(k8sClient.Create(ctx, cr)).To(Succeed())
        DeferCleanup(func() { _ = k8sClient.Delete(ctx, cr) })

        Eventually(func(g Gomega) {
            var fetched autoscalingv1alpha1.AgenticAutoscaler
            g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &fetched)).To(Succeed())
            g.Expect(fetched.Status.Phase).To(Equal("Disabled"))
        }).Should(Succeed())
    })
})
```

- [ ] **Step 4: Run envtest**

```bash
go test ./internal/controller/... -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/controller/
git commit -m "feat(controller): reconciler pre-checks (kill-switch, deletion)"
```

---

### Task 12: Query Prometheus + POST to Forecast Service (happy path)

**Files:**
- Modify: `internal/controller/agenticautoscaler_controller.go`
- Modify: `internal/controller/agenticautoscaler_controller_test.go`

- [ ] **Step 1: Extend Reconcile to call Prometheus and Forecast**

After the pre-check block, add:

```go
    // Step 2: query Prometheus.
    deployName := aas.Spec.TargetRef.Name
    currentRPS, err := r.PromQuerier.InstantQuery(ctx, promql.InstantRPS(deployName))
    if err != nil {
        r.EventRecorder.Event(&aas, corev1.EventTypeWarning, "MetricsUnavailable", reasoning.MetricsUnavailable)
        log.Error(err, "prometheus instant query failed")
        return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
    }

    historyStart := time.Now().Add(-time.Duration(r.Config.HotPathHistoryMinutes) * time.Minute)
    samples, err := r.PromQuerier.RangeQuery(ctx, promql.RangeRPS(deployName), historyStart, time.Now(), time.Minute)
    if err != nil {
        r.EventRecorder.Event(&aas, corev1.EventTypeWarning, "MetricsUnavailable", reasoning.MetricsUnavailable)
        return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
    }
    if len(samples) < int(r.Config.HotPathMinPoints) {
        r.EventRecorder.Event(&aas, corev1.EventTypeWarning, "MetricsUnavailable", reasoning.MetricsUnavailable)
        return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
    }

    // Build rps_history slice.
    rpsHistory := make([]float64, len(samples))
    for i, s := range samples { rpsHistory[i] = s.Value }

    // Step 3: POST to forecast.
    effectiveParams := decision.ResolveEffectiveParams(r.buildParamSources(&aas))
    preferredModel := effectiveParams.Forecaster
    if preferredModel == "auto" { preferredModel = "" }

    forecastResp, err := r.Forecaster.Recommend(ctx, forecast.RecommendRequest{
        RpsHistory:     rpsHistory,
        WorkloadID:     req.NamespacedName.String(),
        PreferredModel: preferredModel,
    })
    if err != nil {
        r.EventRecorder.Event(&aas, corev1.EventTypeWarning, "ForecastUnavailable", reasoning.ForecastUnavailable)
        log.Error(err, "forecast service call failed")
        return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
    }
```

- [ ] **Step 2: Write the envtest spec for happy path**

```go
var _ = Describe("Reconciler happy path", func() {
    It("calls forecast and scales up", func() {
        // This test verifies the full pipeline: Prometheus returns data,
        // forecast returns a prediction, the decision layer computes a
        // target, and the Deployment is scaled.
        // ... (uses fakePromQuerier returning currentRPS=500, 20 range samples,
        //      fakeForecaster returning predicted_rps=1000)
        // Assertion: Deployment replicas go up; Status is updated.
    })
})
```

(Full test body omitted for brevity; it follows the same pattern as T11.)

- [ ] **Step 3: Run envtest**

- [ ] **Step 4: Commit**

```bash
git add internal/controller/
git commit -m "feat(controller): query Prometheus + forecast service in reconcile loop"
```

---

### Task 13: HPA conflict check

**Files:**
- Modify: `internal/controller/agenticautoscaler_controller.go`
- Modify: `internal/controller/agenticautoscaler_controller_test.go`

- [ ] **Step 1: Implement the HPA conflict check**

After kill-switch check, before Prometheus call:

```go
    // Step 1c: HPA conflict check.
    var hpaList autoscalingv2.HorizontalPodAutoscalerList
    if err := r.List(ctx, &hpaList, client.InNamespace(aas.Namespace)); err != nil {
        return ctrl.Result{}, err
    }
    for _, hpa := range hpaList.Items {
        if hpa.Spec.ScaleTargetRef.Kind == aas.Spec.TargetRef.Kind &&
            hpa.Spec.ScaleTargetRef.Name == aas.Spec.TargetRef.Name {
            aas.Status.Phase = "Conflict"
            aas.Status.ConflictReason = fmt.Sprintf("HPA %s already manages this Deployment", hpa.Name)
            if err := r.Status().Update(ctx, &aas); err != nil {
                return ctrl.Result{}, err
            }
            r.EventRecorder.Event(&aas, corev1.EventTypeWarning, "ConflictDetected", reasoning.ConflictDetected)
            return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
        }
    }
    // Clear conflict if previously set.
    if aas.Status.Phase == "Conflict" {
        aas.Status.Phase = "Ready"
        aas.Status.ConflictReason = ""
    }
```

- [ ] **Step 2: Write envtest spec**

Create an HPA targeting the same Deployment, verify the CR goes to "Conflict" phase. Delete the HPA, verify it returns to "Ready".

- [ ] **Step 3: Run envtest**

- [ ] **Step 4: Commit**

```bash
git add internal/controller/
git commit -m "feat(controller): HPA conflict detection + auto-clear"
```

---

### Task 14: Failure paths (metrics_unavailable, forecast_unavailable, scale patch error)

**Files:**
- Modify: `internal/controller/agenticautoscaler_controller_test.go`

- [ ] **Step 1: Write envtest specs for each failure**

```go
var _ = Describe("Reconciler failure paths", func() {
    It("emits metrics_unavailable when Prometheus returns error", func() {
        // fakePromQuerier.instantErr = errors.New("connection refused")
        // Assert: no replica change, Event recorded with "MetricsUnavailable".
    })

    It("emits metrics_unavailable when history < HOT_PATH_MIN_POINTS", func() {
        // fakePromQuerier.rangeVal = 5 samples (min is 10)
        // Assert: Event recorded.
    })

    It("emits forecast_unavailable when forecast returns error", func() {
        // fakeForecaster.err = errors.New("timeout")
        // Assert: no replica change.
    })

    It("retries on next reconcile when /scale patch fails", func() {
        // The simplest test: make the Deployment not exist or have a mismatched ownerRef.
        // Assert: error logged, status not updated, result still has requeueAfter.
    })
})
```

- [ ] **Step 2: Verify the implementation already handles each path**

The code from T12 already returns early with Events on these paths. The envtest specs are here to lock the behaviour down.

- [ ] **Step 3: Run envtest**

- [ ] **Step 4: Commit**

```bash
git add internal/controller/
git commit -m "test(controller): cover metrics_unavailable, forecast_unavailable, patch-error paths"
```

---

### Task 15: Scale patch + status update + Event emission

**Files:**
- Modify: `internal/controller/agenticautoscaler_controller.go`
- Modify: `internal/controller/agenticautoscaler_controller_test.go`

- [ ] **Step 1: Implement the decision + patch + status update**

After receiving the forecast response:

```go
    // Get current replicas from the Deployment.
    var deploy appsv1.Deployment
    if err := r.Get(ctx, types.NamespacedName{Namespace: aas.Namespace, Name: deployName}, &deploy); err != nil {
        return ctrl.Result{}, err
    }
    currentReplicas := int32(1)
    if deploy.Spec.Replicas != nil {
        currentReplicas = *deploy.Spec.Replicas
    }

    // Initialize/update per-CR state.
    key := req.NamespacedName
    state := r.StateStore.GetOrCreate(key, 10)
    if !state.Initialized {
        decision.InitializeFromStatus(state, r.buildStatusSeed(&aas))
    }

    // Steady-state gate + ring buffer update.
    interval := time.Duration(r.Config.ReconcileIntervalSeconds) * time.Second
    lastScale := laterOf(state.LastScaleUpTime, state.LastScaleDownTime)
    if decision.ShouldUpdateRpsPerPod(currentRPS, currentReplicas, lastScale, time.Now(), interval) {
        state.Observations.Push(currentRPS / float64(currentReplicas))
        state.RpsPerPod = state.Observations.Median()
    }
    rpsPerPod := decision.ClampRpsPerPod(state.RpsPerPod,
        derefOr(aas.Spec.RpsPerPodMin, 50), derefOr(aas.Spec.RpsPerPodMax, 500))

    // Compute recommended (pre-cap, pre-cooldown).
    recommended := decision.ComputeRecommended(
        forecastResp.PredictedRPS, rpsPerPod,
        derefOr(aas.Spec.MinReplicas, 2), derefOr(aas.Spec.MaxReplicas, 10))

    // Apply cap + cooldown.
    capOut := decision.ApplyCapAndCooldown(decision.CapInput{
        Recommended:   recommended,
        Current:       currentReplicas,
        MaxStep:       effectiveParams.MaxStep,
        CooldownUp:    effectiveParams.CooldownUp,
        CooldownDown:  effectiveParams.CooldownDown,
        LastScaleUp:   state.LastScaleUpTime,
        LastScaleDown: state.LastScaleDownTime,
        Now:           time.Now(),
    })

    // Step 8: hysteresis.
    if capOut.ShouldPatch {
        // Step 9: patch /scale.
        scale := &autoscalingv1.Scale{}
        scale.Spec.Replicas = capOut.Target
        if err := r.SubResource("scale").Update(ctx, &deploy, client.WithSubResourceBody(scale)); err != nil {
            log.Error(err, "failed to patch /scale")
            return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
        }
        // Update cooldown timers.
        if capOut.Target > currentReplicas {
            state.LastScaleUpTime = time.Now()
        } else {
            state.LastScaleDownTime = time.Now()
        }
    }

    // Step 10: emit Event.
    r.EventRecorder.Eventf(&aas, corev1.EventTypeNormal, capOut.Reason,
        "current_rps=%.1f predicted_rps=%.1f current=%d target=%d model=%s",
        currentRPS, forecastResp.PredictedRPS, currentReplicas, capOut.Target, forecastResp.ModelUsed)

    // Notify ExplainWorker on replica-changing events.
    if capOut.ShouldPatch {
        r.ExplainNotify.Notify(ExplainRequest{
            Namespace:       aas.Namespace,
            Name:            aas.Name,
            Reason:          capOut.Reason,
            CurrentReplicas: currentReplicas,
            TargetReplicas:  capOut.Target,
            CurrentRPS:      currentRPS,
            PredictedRPS:    forecastResp.PredictedRPS,
            ModelUsed:       forecastResp.ModelUsed,
        })
    }

    // Step 11: update CR status.
    aas.Status.Phase = "Ready"
    aas.Status.CurrentReplicas = capOut.Target
    aas.Status.RecommendedReplicas = recommended
    aas.Status.PredictedRPS = int32(forecastResp.PredictedRPS)
    aas.Status.RpsPerPodCurrent = int32(rpsPerPod)
    if capOut.ShouldPatch {
        aas.Status.LastScaleTime = &metav1.Time{Time: time.Now()}
    }
    if err := r.Status().Update(ctx, &aas); err != nil {
        return ctrl.Result{}, err
    }

    return ctrl.Result{RequeueAfter: r.requeueInterval()}, nil
```

- [ ] **Step 2: Write envtest specs verifying scale-up + status**

```go
It("patches /scale up and writes status", func() {
    // Setup: fakePromQuerier returns currentRPS=500, 20 range samples
    //        fakeForecaster returns predicted_rps=1000
    //        Deployment starts at 2 replicas
    // Assert: Deployment moves to ceil(1000/250)=4 (if rps_per_pod defaults to midpoint 275, ceil(1000/275)=4)
    //         Status.RecommendedReplicas = 4
    //         Status.PredictedRPS = 1000
    //         Status.Phase = "Ready"
    //         ExplainNotifier received a request
})
```

- [ ] **Step 3: Run envtest**

- [ ] **Step 4: Commit**

```bash
git add internal/controller/
git commit -m "feat(controller): scale patch + status update + Event + ExplainNotify"
```

---

### Task 16: Drop-and-replace channel semantics test

**Files:**
- Modify: `internal/controller/interfaces.go` (already done T10)
- Create: `internal/controller/channel_test.go`

- [ ] **Step 1: Write a unit test for ChannelNotifier**

```go
package controller_test

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"

    controller "github.com/pratyush-ghosh/agentic-autoscaler/internal/controller"
)

func TestChannelNotifier_DropAndReplace(t *testing.T) {
    ch := make(chan controller.ExplainRequest, 1)
    cn := controller.ChannelNotifier{Ch: ch}

    // Fill with stale event.
    cn.Notify(controller.ExplainRequest{Reason: "scale_up", TargetReplicas: 5})
    // Replace with newer event.
    cn.Notify(controller.ExplainRequest{Reason: "scale_up", TargetReplicas: 8})

    // Only the newer event should be in the channel.
    req := <-ch
    assert.Equal(t, int32(8), req.TargetReplicas)

    // Channel is now empty.
    select {
    case <-ch:
        t.Fatal("channel should be empty")
    default:
    }
}

func TestChannelNotifier_EmptyChannel(t *testing.T) {
    ch := make(chan controller.ExplainRequest, 1)
    cn := controller.ChannelNotifier{Ch: ch}

    cn.Notify(controller.ExplainRequest{Reason: "scale_down", TargetReplicas: 2})

    req := <-ch
    require.Equal(t, "scale_down", req.Reason)
}
```

- [ ] **Step 2: Run; verify pass**

- [ ] **Step 3: Commit**

```bash
git add internal/controller/
git commit -m "test(controller): verify drop-and-replace ExplainNotifier channel semantics"
```

---

## Phase 4 — Final smoke + milestone

### Task 17: Lint + full test pass

**Files:** none

- [ ] **Step 1: Lint**

```bash
go vet ./...
go test ./...
```

Expected: clean.

- [ ] **Step 2: Coverage on the decision package**

```bash
go test ./internal/decision/... -coverprofile=/tmp/decision.cov
go tool cover -func=/tmp/decision.cov | tail -1
```

Expected: ≥ 95%.

- [ ] **Step 3: Milestone commit**

```bash
git commit --allow-empty -m "milestone: Plan #4 (reconciler hot path) complete

Decision layer (internal/decision/):
- ResolveEffectiveParams: spec ?? classified ?? defaults nil-coalesce chain
- ComputeRecommended: ceil(predicted/rps_per_pod) clamped to [min, max]
- ApplyCapAndCooldown: maxStepSize cap + dual cooldown + hysteresis;
  step_capped_* / cooldown_holding_* token precedence exactly per design ss5
- RingBuffer (cap=10, FIFO, median) + PerCRState + StateStore
- ShouldUpdateRpsPerPod: steady-state gate (current_rps>=10, replicas>=1,
  2x interval since last scale)
- InitializeFromStatus: restart recovery seeding from persisted status fields
- ClampRpsPerPod: [rpsPerPodMin, rpsPerPodMax] guard

Orchestration layer (internal/controller/):
- Kill-switch pre-check sets phase=Disabled + emits event
- HPA conflict detection + auto-clear (list HPAs, match scaleTargetRef)
- Prometheus instant + range query via PromQuerier interface
- Forecast call via Forecaster interface; 'auto' omitted from wire
- /scale subresource patch with hysteresis gate
- K8s Event with reasoning token + structured message
- ExplainNotifier drop-and-replace channel semantics
- Status update: recommendedReplicas is pre-cap value; lastScaleTime persisted
- All failure paths surface typed events: metrics_unavailable, forecast_unavailable
- Fakes for envtest: fakePromQuerier, fakeForecaster, fakeExplainNotifier
"
```

---

## Plan-specific Definition of Done

- [ ] `go test ./internal/decision/... -v -count=1 -cover` shows all tests passing; coverage ≥ 95%.
- [ ] `go test ./internal/promql/... -v -count=1` passes.
- [ ] `go test ./internal/controller/... -v -count=1` passes (envtest specs for kill-switch, HPA conflict, happy-path scale-up, all failure modes, drop-and-replace channel).
- [ ] `go vet ./...` clean.
- [ ] Decision layer has **zero** Kubernetes imports beyond the CRD spec types — enforced by import analysis in the test file header.
- [ ] `ChannelNotifier` drop-and-replace test proves only the newest event survives.
- [ ] `status.recommendedReplicas` is the pre-cap value (verified by an envtest assertion where cap clips but status shows the unclipped recommendation).
- [ ] Cooldown precedence: when cap + cooldown both apply, `cooldown_holding_*` is the emitted reasoning token and `ShouldPatch = false` (test in T5).

---

## Notes on what's intentionally deferred

- **ClassifierWorker writing `status.classifiedParams`** — Plan #5. The reconciler only reads it.
- **ExplainWorker consuming the channel** — Plan #6. This plan only sends to it.
- **RBAC Role refinement** — Plan #9. kubebuilder markers are added here; the final role.yaml is generated in Plan #9's `make manifests` pass.
- **Metrics (prometheus instrumentation) on the reconciler** — Plan #10's observability pass.
- **Integration with a real Prometheus + Forecast Service** — Plan #10's smoke E2E.
- **Requeueing period logic** — uses `requeueInterval()` helper returning `time.Duration(r.Config.ReconcileIntervalSeconds) * time.Second`; the timer-drift concern is addressed in Plan #10 by an offset-randomization helper (jitter).

---

## Self-Review (Spec Coverage, Placeholders, Type Consistency)

**Spec coverage.** Every numbered step in design §5 is represented in the Spec Coverage Map. The failure rows from §9 (Prometheus, Forecast, /scale patch) are covered by T14. The "CR has no classifiedParams yet" row is satisfied by T3's nil Classified test case.

**Placeholders.** The envtest spec bodies in T12/T14 are abbreviated for plan readability but contain enough structure to implement from (fakePromQuerier/Forecaster setup pattern, Gomega Eventually assertions). The implementation code blocks in T15 are full Go — no pseudocode.

**Type consistency.**

- `decision.ParamSources.Spec` fields map 1:1 to `AgenticAutoscalerSpec` pointer fields from Plan #1: `ScaleUpCooldownSeconds`, `ScaleDownCooldownSeconds`, `MaxStepSize`, `PreferredForecaster`.
- `decision.CapInput.CooldownUp/Down` are `int32` seconds, matching `effectiveParams.CooldownUp/Down` which came from the same type chain.
- `decision.ApplyCapAndCooldown` return's `Reason` strings match `internal/reasoning/tokens.go` constants from Plan #1: `"scale_up"`, `"scale_down"`, `"no_change"`, `"step_capped_up"`, `"step_capped_down"`, `"cooldown_holding_up"`, `"cooldown_holding_down"`.
- `ExplainRequest` fields match what Plan #6's ExplainWorker will need: namespace, name, reason, replicas, rps, model.
- `PromQuerier` interface method signatures match `prometheus.Client` from Plan #3.
- `Forecaster` interface method signature matches `forecast.Client.Recommend` from Plan #3.
- Status fields written in T15 (`Phase`, `CurrentReplicas`, `RecommendedReplicas`, `PredictedRPS`, `RpsPerPodCurrent`, `LastScaleTime`) match the CRD types from Plan #1 T7.

---

## Execution handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-24-plan-04-reconciler-hot-path.md`. Execution options:

1. **Subagent-Driven (recommended)** — dispatch a subagent per task.
2. **Inline Execution** — execute tasks in this session with checkpoints.

Which approach?
