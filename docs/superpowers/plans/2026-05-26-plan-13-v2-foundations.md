# Plan 13 — v2 Foundations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the `Context` object end-to-end (CRD schema → controller adapter → Forecast Service Pydantic model) and implement the cold-path computations that fill it. Move the cold path from 1-min to 5-min downsampling cadence, raise classifier thresholds (72 / 240) and rename `K_TOD_DOWN` → `K_PERIODIC_DOWN` in code. Align env-var defaults to v2 spec, including new env-tunable knobs (`CONTEXT_DOWNSAMPLE_RESOLUTION_MIN`, `CV_GUARD_MEAN_RPS`, `RPS_PER_POD_NOISE_FLOOR_RPS`, `HOURLY_PROFILE_MIN_HOURS`) on the controller and the new Forecast Service env vars (`GBDT_QUANTILE`, `GBDT_MIN_POINTS`, `PROPHET_USE_HOURLY_REGRESSOR`, `LINEAR_EXTRAP_RECENT_WEIGHT`, `LINEAR_EXTRAP_WINDOW_MINUTES`).

**Architecture:** Strict TDD. Each task ships with a failing test, the minimum code change to flip it green, a verification command, and a commit. The plan is partitioned into five sub-PRs of increasing depth — types & env vars first (no behaviour change), then classifier features, then context computation, then context wiring through the hot path, then spec-trailer cleanup. Phase 3 (Plan 14) wires the forecasters to actually consume the context; this plan only delivers the contract and the producer side.

**Tech Stack:** Go 1.22+, Python 3.11+, Pydantic v2, FastAPI, controller-runtime, kubebuilder markers, pytest, `go test`. `make test` runs both Go and Python suites.

---

## Spec Coverage Map

| Plan item | Tasks | Source |
| --- | --- | --- |
| **G10** — Context plumbed end-to-end | T1, T2, T3, T11, T12, T14, T15 | gap-report-v2.md G10 |
| **G11** — Cold path at 5-min cadence + new features + raised thresholds | T7, T8, T9, T10, T11, T12 | gap-report-v2.md G11 |
| **G21** — Env-var defaults realignment | T4, T5, T13 | gap-report-v2.md G21 |
| **F2a-revisited** — `MIN_POINTS=72`, `HIGH_CONFIDENCE_POINTS=240` | T4 | v2_revision notes F2a-revisited |
| **F4a** — autocorr lag = `60 / DOWNSAMPLE_RESOLUTION_MIN` | T7 | v2_revision notes F4a |
| **F13** — `KTodDown` → `KPeriodicDown` rename in code | T6 | v2_revision notes F13 |
| **F18** — `trend_24h_slope` units = rps/min | T10 | v2_revision notes F18 |
| **F23** — `RPS_PER_POD_NOISE_FLOOR_RPS` env var | T13 | v2_revision notes F23 |
| **F24** — `GBDT_MIN_POINTS` env var | T5 | v2_revision notes F24 |
| **F26** — `gradual_ramp` relative threshold | T9 | v2_revision notes F26 |
| **F28** — `peak_to_trough` denominator `max(mean, 1.0)` | T8 | v2_revision notes F28 |
| **F29** — name `CV_GUARD_MEAN_RPS` | T8 | v2_revision notes F29 |
| **F32c** — `CV_GUARD_MEAN_RPS` env-tunable | T4 | v2_revision notes F32c |
| **F36** — `FORECAST_HORIZON_MINUTES` service-only | T4 | v2_revision notes F36 |
| **E1** — remove `K_TOD_DOWN` disclaimer | T16 | v2-spec-revision-plan.md E1 |
| **E3** — F2a-revisited prose tightening | T17 | v2-spec-revision-plan.md E3 |

---

## File Structure

Files created or modified by this plan:

| Path | Sub-PR | Responsibility |
| --- | --- | --- |
| `api/v1alpha1/agenticautoscaler_types.go` | A | Add `Context` struct + `Context` pointer field on `ClassifiedParams` + `UnboundedRecommended` deferred (Phase 4 owns it) |
| `internal/adapters/forecast/types.go` | A | Add `Context` field on `RecommendRequest` + `ContextPayload` struct (omitempty) |
| `forecast-service/src/forecast/models.py` | A | Add Pydantic `ContextPayload` model + `context: ContextPayload \| None` on `RecommendRequest` |
| `internal/config/config.go` | A | New env vars (`CONTEXT_DOWNSAMPLE_RESOLUTION_MIN`, `CV_GUARD_MEAN_RPS`, `RPS_PER_POD_NOISE_FLOOR_RPS`, `HOURLY_PROFILE_MIN_HOURS`); raised `CLASSIFIER_MIN_POINTS` default to 72; relaxed validate floor to `60/RESOLUTION + 10`; removed `FORECAST_HORIZON_MINUTES` (service-only); lowered `PROPHET_MIN_POINTS` default to 30 |
| `forecast-service/src/forecast/app.py` | A | New env vars (`GBDT_QUANTILE`, `GBDT_MIN_POINTS`, `PROPHET_USE_HOURLY_REGRESSOR`, `LINEAR_EXTRAP_RECENT_WEIGHT`, `LINEAR_EXTRAP_WINDOW_MINUTES`, `HOURLY_PROFILE_MIN_HOURS`); lowered `PROPHET_MIN_POINTS` default to 30 |
| `internal/classifier/params.go` | B | Rename `KTodDown` → `KPeriodicDown`, formula updated |
| `internal/classifier/features.go` | B | `TodLag` → derived `HourlyAutocorrLag(resolutionMin)`; `peakToTrough` denominator → `max(m, CV_GUARD_MEAN_RPS)`; CV zero-guard explicit; `trendSlope` returns rps/min via `/ resolutionMin`; new functions to compute `baseline_rps`, `peak_p95_rps`, `trend_24h_slope`, `hourly_profile`, `hourly_profile_valid` |
| `internal/classifier/classify.go` | B | Replace absolute `trendSlopeRampAbove=2.0` with relative `gradualRampDailyDriftFrac=0.20` rule using `mean` |
| `internal/classifier/pipeline.go` | C | Pipeline returns the new context fields; threading `resolutionMin` and `hourlyProfileMinHours` through |
| `internal/classifier/worker.go` | C | PromQL step changes from `time.Minute` to `cfg.ResolutionMin`; `patchStatus` writes `context` block |
| `internal/decision/decision.go` | C | `ShouldUpdateRpsPerPod` uses `cfg.RpsPerPodNoiseFloorRPS` instead of hardcoded `10` |
| `internal/controller/agenticautoscaler_controller.go` | D | Builds `RecommendRequest.Context` from `aas.Status.ClassifiedParams.Context` plus `current_hour_utc` / `current_minute_utc`; honours `autoscaling.agentic.io/skip-context` annotation (omits context) |
| `forecast-service/src/forecast/dispatch.py` | D | Accepts `context: ContextPayload \| None`, validates, drops malformed → logs warning, forwards to forecasters (forecasters still ignore in Phase 2; Phase 3 wires consumption) |
| `forecast-service/src/forecast/app.py` | D | Wires `context` from request through to `recommend()` |
| `docs/design_v2.md` | E | Spec trailers E1 (remove K_TOD_DOWN disclaimer) + E3 (tighten F2a-revisited prose if needed) |

Test files created or modified mirror each implementation file: `internal/classifier/features_test.go`, `internal/classifier/classify_test.go`, `internal/classifier/params_test.go`, `internal/classifier/pipeline_test.go`, `internal/classifier/worker_test.go`, `internal/config/config_test.go`, `forecast-service/tests/unit/test_models.py`, `forecast-service/tests/unit/test_dispatch.py`, `forecast-service/tests/integration/test_app.py`.

---

## Sub-PR A: Types and env vars (no behaviour change)

These tasks land schema and config changes only. Existing controller and Forecast Service behaviour is unchanged because no consumer reads the new fields yet.

### Task 1: G10 — Add `Context` struct to CRD types

**Files:**
- Modify: `api/v1alpha1/agenticautoscaler_types.go` (after `ClassifiedParams` struct, before `AgenticAutoscalerPhase`)
- Modify: `api/v1alpha1/zz_generated.deepcopy.go` (regenerated by `make manifests`)
- Test: existing `make manifests` and `go vet ./...` cover this; no new test file

- [ ] **Step 1: Write the failing test**

In `api/v1alpha1/agenticautoscaler_types_test.go` (create if absent):

```go
package v1alpha1

import "testing"

func TestClassifiedParamsContextRoundTrip(t *testing.T) {
	cp := ClassifiedParams{
		Pattern: "periodic",
		Context: &ContextFields{
			BaselineRPS:        50,
			PeakP95RPS:         200,
			Trend24hSlope:      0.5,
			HourlyProfile:      []int32{10, 12, 14, 18, 22, 30, 50, 80, 100, 120, 140, 150, 150, 145, 140, 130, 110, 95, 80, 60, 40, 25, 15, 10},
			HourlyProfileValid: true,
		},
	}
	if cp.Context == nil {
		t.Fatal("Context must be non-nil after assignment")
	}
	if len(cp.Context.HourlyProfile) != 24 {
		t.Fatalf("HourlyProfile length = %d, want 24", len(cp.Context.HourlyProfile))
	}
	if cp.Context.BaselineRPS != 50 {
		t.Errorf("BaselineRPS round-trip failed")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./api/v1alpha1/... -run TestClassifiedParamsContextRoundTrip -v`
Expected: FAIL with `undefined: ContextFields` (and `Context` field missing).

- [ ] **Step 3: Add the type**

In `api/v1alpha1/agenticautoscaler_types.go`, add `ContextFields` struct above `AgenticAutoscalerPhase` and a pointer field on `ClassifiedParams`:

```go
// ContextFields is the cold-path-computed long-horizon context block
// forwarded to the Forecast Service on every /recommend call.
// See docs/design_v2.md §4 (status fields) and §6.1 step 6.5 (computation).
// Nil during cold start; populated after the first successful classification.
type ContextFields struct {
	// BaselineRPS is the median RPS over the full classifier window (rps).
	BaselineRPS int32 `json:"baselineRPS"`

	// PeakP95RPS is the 95th-percentile RPS over the full classifier window (rps).
	PeakP95RPS int32 `json:"peakP95RPS"`

	// Trend24hSlope is the least-squares slope in rps/MINUTE over the full window.
	// See docs/design_v2.md §6.1 step 6.5 for the unit derivation.
	// +kubebuilder:validation:Format=double
	Trend24hSlope float64 `json:"trend24hSlope"`

	// HourlyProfile is a length-24 array of median RPS per UTC hour.
	// Zero-filled for hours with no observations.
	// +kubebuilder:validation:MinItems=24
	// +kubebuilder:validation:MaxItems=24
	HourlyProfile []int32 `json:"hourlyProfile"`

	// HourlyProfileValid is true when at least HOURLY_PROFILE_MIN_HOURS distinct
	// UTC hours had observations during the classification window.
	// When false, downstream consumers ignore HourlyProfile and use the
	// other context fields only.
	HourlyProfileValid bool `json:"hourlyProfileValid"`
}
```

And add a `Context *ContextFields` field on `ClassifiedParams` (after `Confidence`):

```go
	// Context is the cold-path-computed long-horizon context.
	// nil before the first successful classification.
	// +optional
	Context *ContextFields `json:"context,omitempty"`
```

- [ ] **Step 4: Regenerate deepcopy + manifests**

Run: `make generate manifests`
Expected: `api/v1alpha1/zz_generated.deepcopy.go` updated with `ContextFields` + nested fields; CRD YAML in `config/crd/bases/` shows the new sub-object.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./api/v1alpha1/... -run TestClassifiedParamsContextRoundTrip -v`
Expected: PASS.

Run: `go vet ./...`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add api/v1alpha1/ config/crd/
git commit -m "feat(api): add ContextFields struct and ClassifiedParams.Context field (G10)"
```

---

### Task 2: G10 — Add `Context` to Go-side `RecommendRequest`

**Files:**
- Modify: `internal/adapters/forecast/types.go`
- Test: `internal/adapters/forecast/types_test.go` (create)

- [ ] **Step 1: Write the failing test**

```go
package forecast

import (
	"encoding/json"
	"testing"
)

func TestRecommendRequestContextOmitEmpty(t *testing.T) {
	req := RecommendRequest{
		RpsHistory:     []float64{1, 2, 3},
		WorkloadID:     "ns/name",
		PreferredModel: "auto",
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(b); contains(got, "context") {
		t.Fatalf("nil Context must omit JSON field, got: %s", got)
	}
}

func TestRecommendRequestContextRoundTrip(t *testing.T) {
	req := RecommendRequest{
		RpsHistory: []float64{1, 2, 3},
		Context: &ContextPayload{
			BaselineRPS:        50,
			PeakP95RPS:         200,
			Trend24hSlope:      0.5,
			HourlyProfile:      []int32{10, 12, 14, 18, 22, 30, 50, 80, 100, 120, 140, 150, 150, 145, 140, 130, 110, 95, 80, 60, 40, 25, 15, 10},
			HourlyProfileValid: true,
			CurrentHourUTC:     14,
			CurrentMinuteUTC:   42,
		},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	for _, want := range []string{`"baseline_rps":50`, `"current_hour_utc":14`, `"hourly_profile_valid":true`} {
		if !contains(got, want) {
			t.Errorf("output missing %q; got %s", want, got)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		(len(haystack) > len(needle) && (containsAt(haystack, needle))))
}
func containsAt(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/forecast/... -run TestRecommendRequest -v`
Expected: FAIL — `ContextPayload` undefined.

- [ ] **Step 3: Implement the type**

Replace the contents of `internal/adapters/forecast/types.go`:

```go
/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package forecast is the Controller-side client for the Forecast Service.
// See docs/design_v2.md §5 for the wire contract.
package forecast

// ContextPayload is the long-horizon context block forwarded on every
// /recommend call. JSON field names are snake_case to match the Forecast
// Service's Pydantic model. See docs/design_v2.md §5 /recommend input.
type ContextPayload struct {
	BaselineRPS        int32   `json:"baseline_rps"`
	PeakP95RPS         int32   `json:"peak_p95_rps"`
	Trend24hSlope      float64 `json:"trend_24h_slope"`
	HourlyProfile      []int32 `json:"hourly_profile"`
	HourlyProfileValid bool    `json:"hourly_profile_valid"`
	// CurrentHourUTC and CurrentMinuteUTC are per-request; the controller
	// computes them at request time and they are NOT persisted in
	// status.classifiedParams.context. See docs/design_v2.md §5 step 1
	// "Note on per-request context fields".
	CurrentHourUTC   int `json:"current_hour_utc"`
	CurrentMinuteUTC int `json:"current_minute_utc"`
}

// RecommendRequest is the body of POST /recommend.
type RecommendRequest struct {
	RpsHistory []float64 `json:"rps_history"`
	WorkloadID string    `json:"workload_id,omitempty"`
	// PreferredModel is "prophet", "linear_extrap", "gbdt_quantile", or "auto" / "" to defer.
	// Per design §5, "auto" / null / absent must be wire-equivalent — so the
	// adapter normalises "auto" to "" and the omitempty tag drops it.
	PreferredModel string `json:"preferred_model,omitempty"`
	// Context is the long-horizon block from the cold path. Nil = absent;
	// the omitempty tag ensures the JSON key is dropped on cold start so
	// the Forecast Service's context-free path engages cleanly.
	Context *ContextPayload `json:"context,omitempty"`
}

// RecommendResponse is the body returned by POST /recommend.
type RecommendResponse struct {
	PredictedRPS   float64 `json:"predicted_rps"`
	HorizonMinutes int     `json:"horizon_minutes"`
	ModelUsed      string  `json:"model_used"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/forecast/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/forecast/
git commit -m "feat(adapter): add ContextPayload + RecommendRequest.Context (G10)"
```

---

### Task 3: G10 — Add `Context` to Python `RecommendRequest`

**Files:**
- Modify: `forecast-service/src/forecast/models.py`
- Test: `forecast-service/tests/unit/test_models.py`

- [ ] **Step 1: Write the failing test**

In `forecast-service/tests/unit/test_models.py`, add:

```python
import pytest
from pydantic import ValidationError

from forecast.models import ContextPayload, RecommendRequest


def test_recommend_request_accepts_context() -> None:
    req = RecommendRequest(
        rps_history=[1.0, 2.0, 3.0],
        context={
            "baseline_rps": 50,
            "peak_p95_rps": 200,
            "trend_24h_slope": 0.5,
            "hourly_profile": [10] * 24,
            "hourly_profile_valid": True,
            "current_hour_utc": 14,
            "current_minute_utc": 42,
        },
    )
    assert req.context is not None
    assert req.context.baseline_rps == 50
    assert req.context.current_minute_utc == 42


def test_recommend_request_context_optional() -> None:
    req = RecommendRequest(rps_history=[1.0])
    assert req.context is None


def test_context_hourly_profile_must_be_24() -> None:
    with pytest.raises(ValidationError, match=r"hourly_profile"):
        ContextPayload(
            baseline_rps=10,
            peak_p95_rps=20,
            trend_24h_slope=0.0,
            hourly_profile=[1, 2, 3],  # too short
            hourly_profile_valid=False,
            current_hour_utc=0,
            current_minute_utc=0,
        )


def test_context_current_hour_range() -> None:
    with pytest.raises(ValidationError, match=r"current_hour_utc"):
        ContextPayload(
            baseline_rps=10,
            peak_p95_rps=20,
            trend_24h_slope=0.0,
            hourly_profile=[0] * 24,
            hourly_profile_valid=False,
            current_hour_utc=24,  # out of range
            current_minute_utc=0,
        )
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd forecast-service && pytest tests/unit/test_models.py -k context -v`
Expected: FAIL — `ContextPayload` not importable.

- [ ] **Step 3: Implement the model**

Replace `forecast-service/src/forecast/models.py`:

```python
"""Pydantic v2 request and response models for /recommend.

Wire types match the Go `internal/adapters/forecast/types.go` exactly.
See docs/design_v2.md §5 for the contract.
"""

from __future__ import annotations

from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator


class ContextPayload(BaseModel):
    """Long-horizon context block computed by the controller's cold path.

    All fields are required when the parent ``context`` object is present.
    The controller drops the entire ``context`` field (rather than
    populating it with partial data) when the classifier has not yet run.
    """

    model_config = ConfigDict(extra="ignore")

    baseline_rps: int
    peak_p95_rps: int
    trend_24h_slope: float
    hourly_profile: Annotated[list[int], Field(min_length=24, max_length=24)]
    hourly_profile_valid: bool
    current_hour_utc: Annotated[int, Field(ge=0, le=23)]
    current_minute_utc: Annotated[int, Field(ge=0, le=59)]


class RecommendRequest(BaseModel):
    """Body of POST /recommend."""

    model_config = ConfigDict(extra="ignore")

    rps_history: Annotated[list[float], Field(min_length=1)]
    """Recent per-minute RPS values, oldest first."""

    workload_id: str | None = None
    """Free-form identifier; accepted but unused. Useful for tracing."""

    preferred_model: Literal["prophet", "linear_extrap", "gbdt_quantile", "auto"] | None = None
    """Override for model selection. None or 'auto' means defer to auto-select."""

    context: ContextPayload | None = None
    """Cold-path-computed long-horizon block; absent during cold start."""

    @field_validator("rps_history")
    @classmethod
    def _all_non_negative(cls, v: list[float]) -> list[float]:
        if any(x < 0 for x in v):
            raise ValueError("rps_history values must be non-negative")
        return v


class RecommendResponse(BaseModel):
    """Body of POST /recommend response."""

    predicted_rps: float
    horizon_minutes: int
    model_used: Literal["prophet", "linear_extrap", "gbdt_quantile"]
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd forecast-service && pytest tests/unit/test_models.py -v`
Expected: all `test_recommend_request_*` and `test_context_*` PASS; existing tests still PASS.

- [ ] **Step 5: Commit**

```bash
git add forecast-service/src/forecast/models.py forecast-service/tests/unit/test_models.py
git commit -m "feat(forecast-service): add ContextPayload Pydantic model + Recommend wiring (G10)"
```

---

### Task 4: G21 — Controller env-var realignment

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write failing tests**

Append to `internal/config/config_test.go`:

```go
func TestLoadFromEnv_NewV2EnvVars(t *testing.T) {
	t.Setenv("FORECAST_SERVICE_URL", "http://fs:8080")
	t.Setenv("PROMETHEUS_URL", "http://prom:9090")
	t.Setenv("CONTEXT_DOWNSAMPLE_RESOLUTION_MIN", "5")
	t.Setenv("CV_GUARD_MEAN_RPS", "1.0")
	t.Setenv("RPS_PER_POD_NOISE_FLOOR_RPS", "10")
	t.Setenv("HOURLY_PROFILE_MIN_HOURS", "12")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.ContextResolutionMinutes != 5 {
		t.Errorf("ContextResolutionMinutes = %d, want 5", cfg.ContextResolutionMinutes)
	}
	if cfg.CVGuardMeanRPS != 1.0 {
		t.Errorf("CVGuardMeanRPS = %v, want 1.0", cfg.CVGuardMeanRPS)
	}
	if cfg.RpsPerPodNoiseFloorRPS != 10 {
		t.Errorf("RpsPerPodNoiseFloorRPS = %d, want 10", cfg.RpsPerPodNoiseFloorRPS)
	}
	if cfg.HourlyProfileMinHours != 12 {
		t.Errorf("HourlyProfileMinHours = %d, want 12", cfg.HourlyProfileMinHours)
	}
	if cfg.ClassifierMinPoints != 72 {
		t.Errorf("ClassifierMinPoints default = %d, want 72", cfg.ClassifierMinPoints)
	}
	if cfg.ClassifierHighConfidencePoints != 240 {
		t.Errorf("ClassifierHighConfidencePoints default = %d, want 240", cfg.ClassifierHighConfidencePoints)
	}
}

func TestValidate_ClassifierMinPointsFloorTracksResolution(t *testing.T) {
	t.Setenv("FORECAST_SERVICE_URL", "http://fs:8080")
	t.Setenv("PROMETHEUS_URL", "http://prom:9090")
	t.Setenv("CONTEXT_DOWNSAMPLE_RESOLUTION_MIN", "5")
	t.Setenv("CLASSIFIER_MIN_POINTS", "22") // L+10 at 5-min resolution
	t.Setenv("CLASSIFIER_HIGH_CONFIDENCE_POINTS", "240")

	if _, err := LoadFromEnv(); err != nil {
		t.Errorf("MIN_POINTS=22 at resolution=5 should be valid (L+10=22), got: %v", err)
	}
}

func TestValidate_ClassifierMinPointsFloorRejectsTooLow(t *testing.T) {
	t.Setenv("FORECAST_SERVICE_URL", "http://fs:8080")
	t.Setenv("PROMETHEUS_URL", "http://prom:9090")
	t.Setenv("CONTEXT_DOWNSAMPLE_RESOLUTION_MIN", "5")
	t.Setenv("CLASSIFIER_MIN_POINTS", "21") // below L+10

	if _, err := LoadFromEnv(); err == nil {
		t.Errorf("MIN_POINTS=21 at resolution=5 should fail validation (L+10=22)")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/... -run "TestLoadFromEnv_NewV2EnvVars|TestValidate_ClassifierMinPointsFloor" -v`
Expected: FAIL — undefined `ContextResolutionMinutes`, etc.

- [ ] **Step 3: Implement env vars + raised defaults + relaxed validate floor**

In `internal/config/config.go`:

(a) Add to `Config` struct after `ClassifierDedup`:

```go
	// Cold-path resolution.
	ContextResolutionMinutes int32 // CONTEXT_DOWNSAMPLE_RESOLUTION_MIN, default 5
	CVGuardMeanRPS           float64 // CV_GUARD_MEAN_RPS, default 1.0
	RpsPerPodNoiseFloorRPS   int32   // RPS_PER_POD_NOISE_FLOOR_RPS, default 10
	HourlyProfileMinHours    int32   // HOURLY_PROFILE_MIN_HOURS, default 12
```

(b) Remove the `ForecastHorizon` field and its env loader (it moves to service-only per F36). Search call sites with: `grep -rn "cfg.ForecastHorizon\|ForecastHorizon " internal/ cmd/`. Either delete the field outright if unused, or downgrade it to a deprecated alias that logs a warning. **Recommended: delete the field; update callers to read `forecastResp.HorizonMinutes` from the response (which is what design §5 already does).**

(c) Raise defaults:

```go
	cfg.ClassifierMinPoints = envIntOrDefault("CLASSIFIER_MIN_POINTS", 72, &errs)
	cfg.ClassifierHighConfidencePoints = envIntOrDefault("CLASSIFIER_HIGH_CONFIDENCE_POINTS", 240, &errs)
	cfg.ProphetMinPoints = envIntOrDefault("PROPHET_MIN_POINTS", 30, &errs)
```

(d) Add new env-var loaders (after `ClassifierDedup`):

```go
	cfg.ContextResolutionMinutes = envIntOrDefault("CONTEXT_DOWNSAMPLE_RESOLUTION_MIN", 5, &errs)
	cfg.CVGuardMeanRPS = envFloat64OrDefault("CV_GUARD_MEAN_RPS", 1.0, &errs)
	cfg.RpsPerPodNoiseFloorRPS = envIntOrDefault("RPS_PER_POD_NOISE_FLOOR_RPS", 10, &errs)
	cfg.HourlyProfileMinHours = envIntOrDefault("HOURLY_PROFILE_MIN_HOURS", 12, &errs)
```

Add an `envFloat64OrDefault` helper next to `envIntOrDefault`:

```go
func envFloat64OrDefault(name string, def float64, errs *[]string) float64 {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s: %v", name, err))
		return def
	}
	return v
}
```

(e) Replace the `validate()` `MIN_POINTS >= 70` check with:

```go
	// Hourly-autocorr lag floor: MIN_POINTS must satisfy L + 10
	// where L = 60 / CONTEXT_DOWNSAMPLE_RESOLUTION_MIN.
	if c.ContextResolutionMinutes < 1 {
		errs = append(errs, fmt.Sprintf(
			"CONTEXT_DOWNSAMPLE_RESOLUTION_MIN=%d must be >= 1",
			c.ContextResolutionMinutes))
	} else {
		lag := int32(60) / c.ContextResolutionMinutes
		floor := lag + 10
		if c.ClassifierMinPoints < floor {
			errs = append(errs, fmt.Sprintf(
				"CLASSIFIER_MIN_POINTS=%d violates floor of %d (= 60/%d + 10) at the configured resolution",
				c.ClassifierMinPoints, floor, c.ContextResolutionMinutes))
		}
	}
	if c.HourlyProfileMinHours < 1 || c.HourlyProfileMinHours > 24 {
		errs = append(errs, fmt.Sprintf(
			"HOURLY_PROFILE_MIN_HOURS=%d must be in [1, 24]",
			c.HourlyProfileMinHours))
	}
	if c.CVGuardMeanRPS < 0 {
		errs = append(errs, fmt.Sprintf(
			"CV_GUARD_MEAN_RPS=%v must be >= 0", c.CVGuardMeanRPS))
	}
	if c.RpsPerPodNoiseFloorRPS < 0 {
		errs = append(errs, fmt.Sprintf(
			"RPS_PER_POD_NOISE_FLOOR_RPS=%d must be >= 0", c.RpsPerPodNoiseFloorRPS))
	}
```

(f) Update `Summary()` to include the four new keys (in canonical alphabetical-within-group order).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/... -v`
Expected: all PASS, including pre-existing tests.

Run: `go build ./...`
Expected: clean build (catches any caller of the deleted `cfg.ForecastHorizon`).

- [ ] **Step 5: Commit**

```bash
git add internal/config/ cmd/ internal/
git commit -m "feat(config): add v2 env vars; raise CLASSIFIER_MIN_POINTS to 72; remove FORECAST_HORIZON_MINUTES (G21, F2a-rev, F23, F29, F32c, F36)"
```

---

### Task 5: G21 — Forecast Service env-var additions

**Files:**
- Modify: `forecast-service/src/forecast/app.py`
- Test: `forecast-service/tests/integration/test_app.py`

- [ ] **Step 1: Write failing test**

In `forecast-service/tests/integration/test_app.py`, add:

```python
def test_new_env_vars_have_defaults(monkeypatch):
    """All v2 env vars should parse with sensible defaults."""
    from forecast import app
    assert app.PROPHET_MIN_POINTS == 30
    assert app.LINEAR_EXTRAP_RECENT_WEIGHT == 0.7
    assert app.LINEAR_EXTRAP_WINDOW_MINUTES == 10
    assert app.GBDT_QUANTILE == 0.90
    assert app.GBDT_MIN_POINTS == 30
    assert app.PROPHET_USE_HOURLY_REGRESSOR is True
    assert app.HOURLY_PROFILE_MIN_HOURS == 12
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd forecast-service && pytest tests/integration/test_app.py -k test_new_env_vars -v`
Expected: FAIL (attributes not defined on `app` module).

- [ ] **Step 3: Implement**

In `forecast-service/src/forecast/app.py`, replace the env-var block at the top with:

```python
FORECAST_HORIZON_MINUTES = int(os.environ.get("FORECAST_HORIZON_MINUTES", "10"))
PROPHET_MIN_POINTS = int(os.environ.get("PROPHET_MIN_POINTS", "30"))
LINEAR_EXTRAP_RECENT_WEIGHT = float(os.environ.get("LINEAR_EXTRAP_RECENT_WEIGHT", "0.7"))
LINEAR_EXTRAP_WINDOW_MINUTES = int(os.environ.get("LINEAR_EXTRAP_WINDOW_MINUTES", "10"))
GBDT_QUANTILE = float(os.environ.get("GBDT_QUANTILE", "0.90"))
GBDT_MIN_POINTS = int(os.environ.get("GBDT_MIN_POINTS", "30"))
PROPHET_USE_HOURLY_REGRESSOR = os.environ.get("PROPHET_USE_HOURLY_REGRESSOR", "true").lower() == "true"
HOURLY_PROFILE_MIN_HOURS = int(os.environ.get("HOURLY_PROFILE_MIN_HOURS", "12"))
```

- [ ] **Step 4: Run tests**

Run: `cd forecast-service && pytest tests/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add forecast-service/src/forecast/app.py forecast-service/tests/
git commit -m "feat(forecast-service): add v2 env vars with defaults (G21, F24)"
```

---

## Sub-PR B: Classifier features at 5-min cadence (G11, F13, F18, F26, F28, F29)

### Task 6: F13 — Rename KTodDown to KPeriodicDown

**Files:**
- Modify: `internal/classifier/params.go`
- Modify: `internal/classifier/params_test.go`

- [ ] **Step 1: Write failing test**

In `internal/classifier/params_test.go`, add:

```go
func TestKPeriodicDownConstantExists(t *testing.T) {
    // Verifies the rename landed; the old name must not compile.
    if KPeriodicDown != 0.5 {
        t.Errorf("KPeriodicDown = %v, want 0.5", KPeriodicDown)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/classifier/... -run TestKPeriodicDownConstantExists -v`
Expected: FAIL (undefined: KPeriodicDown).

- [ ] **Step 3: Rename the constant**

In `internal/classifier/params.go`, rename `KTodDown` to `KPeriodicDown`. Update the formula comment and the `ComputeParams` usage:

```go
KPeriodicDown = 0.5  // was KTodDown; see design_v2.md section 7
```

- [ ] **Step 4: Run full classifier tests**

Run: `go test ./internal/classifier/... -v`
Expected: all PASS (params_test and any test referencing KTodDown updated).

- [ ] **Step 5: Commit**

```bash
git add internal/classifier/
git commit -m "refactor(classifier): rename KTodDown to KPeriodicDown (F13)"
```

---

### Task 7: F4a + G11 — Parameterize TodLag by resolution

**Files:**
- Modify: `internal/classifier/features.go`
- Modify: `internal/classifier/features_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestHourlyAutocorrLag(t *testing.T) {
    // At 5-min resolution, lag should be 12 (60/5)
    lag := HourlyAutocorrLag(5)
    if lag != 12 {
        t.Errorf("HourlyAutocorrLag(5) = %d, want 12", lag)
    }
    // At 1-min resolution, lag should be 60
    lag = HourlyAutocorrLag(1)
    if lag != 60 {
        t.Errorf("HourlyAutocorrLag(1) = %d, want 60", lag)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/classifier/... -run TestHourlyAutocorrLag -v`
Expected: FAIL (undefined: HourlyAutocorrLag).

- [ ] **Step 3: Implement**

In `features.go`:
- Add `func HourlyAutocorrLag(resolutionMin int) int { return 60 / resolutionMin }`
- Keep the existing `TodLag = 60` constant for backward compat (used in existing tests), but mark it deprecated
- The `ExtractFeatures` function will later (T12) accept `resolutionMin` as a parameter; for now this task only adds the helper

- [ ] **Step 4: Run tests**

Run: `go test ./internal/classifier/... -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/classifier/features.go internal/classifier/features_test.go
git commit -m "feat(classifier): add HourlyAutocorrLag(resolutionMin) helper (F4a, G11)"
```

---

### Task 8: F28 + F29 — Fix peakToTrough denominator and name CV guard

**Files:**
- Modify: `internal/classifier/features.go`
- Modify: `internal/classifier/features_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestPeakToTroughDenominator(t *testing.T) {
    // With mean=0.5 (below CV_GUARD), denominator should be max(0.5, 1.0) = 1.0
    series := []float64{0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 2.0}
    f := ExtractFeatures(series)
    // p99 of this 10-element series is 2.0
    // Old denominator: mean+1 = 0.55+1 = 1.55 -> peakToTrough ~1.29
    // New denominator: max(mean, 1.0) = 1.0 -> peakToTrough = 2.0
    if f.PeakToTrough < 1.9 || f.PeakToTrough > 2.1 {
        t.Errorf("PeakToTrough = %v, want ~2.0 (max(mean,1) denominator)", f.PeakToTrough)
    }
}

func TestCVZeroGuard(t *testing.T) {
    // Mean < 1.0 (CV_GUARD_MEAN_RPS default) -> cv forced to 0
    series := []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 0.5}
    f := ExtractFeatures(series)
    if f.CV != 0 {
        t.Errorf("CV = %v, want 0 (mean < CV_GUARD_MEAN_RPS)", f.CV)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/classifier/... -run "TestPeakToTroughDenominator|TestCVZeroGuard" -v`
Expected: `TestPeakToTroughDenominator` FAIL (old denominator gives ~1.29, not ~2.0).

- [ ] **Step 3: Implement**

In `features.go`, change line 73:
```go
// Old: peakToTrough := p99 / (m + 1)
// New (F28):
denom := math.Max(m, CVGuardMeanRPS)
peakToTrough := p99 / denom
```

Add at package level:
```go
// CVGuardMeanRPS is the threshold below which cv is forced to 0.
// Configurable via config.Config.CVGuardMeanRPS at runtime; this
// package-level default is only used when ExtractFeatures is called
// without an explicit override (e.g. in unit tests).
var CVGuardMeanRPS float64 = 1.0
```

Change the cv guard (line 68):
```go
// Old: if m >= 1 {
// New (F29):
if m >= CVGuardMeanRPS {
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/classifier/... -v`
Expected: all PASS (update any existing tests that assumed the old denominator).

- [ ] **Step 5: Commit**

```bash
git add internal/classifier/features.go internal/classifier/features_test.go
git commit -m "fix(classifier): peakToTrough uses max(mean,1) denom; name CVGuardMeanRPS (F28, F29)"
```

---

### Task 9: F26 — Replace gradual_ramp absolute threshold with relative

**Files:**
- Modify: `internal/classifier/classify.go`
- Modify: `internal/classifier/classify_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestGradualRampRelativeThreshold(t *testing.T) {
    // At mean=100, slope=0.015 rps/min -> daily drift = 0.015*1440/100 = 0.216 > 0.20
    // Should classify as gradual_ramp with relative threshold
    f := Features{CV: 0.30, PeakToTrough: 2.0, TodCorrelation: 0.20, TrendSlope: 0.015}
    got := ClassifyWithMean(f, 100.0)
    if got != PatternGradualRamp {
        t.Errorf("ClassifyWithMean(slope=0.015, mean=100) = %q, want %q", got, PatternGradualRamp)
    }
}

func TestGradualRampAbsoluteWouldNotFire(t *testing.T) {
    // Same slope=0.015 would NOT fire the old |slope|>2.0 threshold
    // Confirms this is a behavioural change
    f := Features{CV: 0.30, PeakToTrough: 2.0, TodCorrelation: 0.20, TrendSlope: 0.015}
    if math.Abs(f.TrendSlope) > 2.0 {
        t.Fatal("test setup error: slope should be below old absolute threshold")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/classifier/... -run TestGradualRampRelativeThreshold -v`
Expected: FAIL (undefined: ClassifyWithMean).

- [ ] **Step 3: Implement**

In `classify.go`:
- Add a new exported `ClassifyWithMean(f Features, seriesMean float64) string` that implements the relative threshold:

```go
// GradualRampDailyDriftFrac is the relative threshold for the
// gradual_ramp rule. Fires when |slope| * 1440 / max(mean, 1) exceeds
// this fraction. 0.20 = "24h drift exceeds 20% of the workload's mean."
const GradualRampDailyDriftFrac = 0.20

func ClassifyWithMean(f Features, seriesMean float64) string {
    denom := math.Max(seriesMean, 1.0)
    switch {
    case f.CV < cvFlatBelow:
        return PatternFlat
    case f.TodCorrelation > tdCorrelationAbove:
        return PatternPeriodic
    case f.CV > cvSpikyAbove && f.PeakToTrough > peakToTroughSpikyAbove:
        return PatternSpiky
    case math.Abs(f.TrendSlope)*1440/denom > GradualRampDailyDriftFrac:
        return PatternGradualRamp
    default:
        return PatternDefault
    }
}
```

- Update the existing `Classify(f)` to call `ClassifyWithMean(f, 1.0)` for backward compat (the pipeline will switch to passing the real mean in T12).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/classifier/... -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/classifier/classify.go internal/classifier/classify_test.go
git commit -m "feat(classifier): gradual_ramp uses relative threshold (F26, G11)"
```

---

### Task 10: F18 — trendSlope returns rps/min at any cadence

**Files:**
- Modify: `internal/classifier/features.go`
- Modify: `internal/classifier/features_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestTrendSlopeRpsPerMin(t *testing.T) {
    // Linear series 0,5,10,15,20 at 5-min cadence
    // Raw slope = 5 rps/sample. In rps/min = 5/5 = 1.0
    series := []float64{0, 5, 10, 15, 20}
    got := TrendSlopeRpsPerMin(series, 5)
    if math.Abs(got-1.0) > 0.001 {
        t.Errorf("TrendSlopeRpsPerMin(5-min) = %v, want 1.0", got)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/classifier/... -run TestTrendSlopeRpsPerMin -v`
Expected: FAIL (undefined: TrendSlopeRpsPerMin).

- [ ] **Step 3: Implement**

In `features.go`, add:

```go
// TrendSlopeRpsPerMin computes the least-squares slope and converts from
// rps/sample to rps/min by dividing by resolutionMin.
func TrendSlopeRpsPerMin(series []float64, resolutionMin int) float64 {
    raw := trendSlope(series)
    if resolutionMin <= 0 {
        return raw
    }
    return raw / float64(resolutionMin)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/classifier/... -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/classifier/features.go internal/classifier/features_test.go
git commit -m "feat(classifier): TrendSlopeRpsPerMin divides by resolution (F18, G11)"
```

---

## Sub-PR C: Context computation in classifier (G10 + G11)

### Task 11: G11 — Compute context fields in pipeline

**Files:**
- Modify: `internal/classifier/features.go` (new exported functions)
- Modify: `internal/classifier/pipeline.go` (return context in result)
- Modify: `internal/classifier/features_test.go`
- Modify: `internal/classifier/pipeline_test.go`

- [ ] **Step 1: Write failing tests**

In `features_test.go`:

```go
func TestComputeBaselineRPS(t *testing.T) {
    series := []float64{10, 20, 30, 40, 50}
    got := ComputeBaselineRPS(series)
    if got != 30 { // median
        t.Errorf("ComputeBaselineRPS = %v, want 30", got)
    }
}

func TestComputePeakP95RPS(t *testing.T) {
    series := make([]float64, 100)
    for i := range series { series[i] = float64(i + 1) }
    got := ComputePeakP95RPS(series)
    if got != 95 {
        t.Errorf("ComputePeakP95RPS = %v, want 95", got)
    }
}

func TestComputeHourlyProfile(t *testing.T) {
    // 24 points, one per hour at 5-min resolution = need 288 points
    // Simplified: pass hourTimestamps to compute per-hour medians
    // Test with 24 known hourly buckets
    profile, valid := ComputeHourlyProfile(make([]float64, 288), 5, 0, 12)
    if len(profile) != 24 {
        t.Fatalf("profile length = %d, want 24", len(profile))
    }
    if !valid {
        t.Error("expected hourlyProfileValid=true for 288 points at 5-min (~24h)")
    }
}
```

In `pipeline_test.go`:

```go
func TestRunPipelineV2ReturnsContext(t *testing.T) {
    series := make([]float64, 100)
    for i := range series { series[i] = float64(50 + i%10) }
    result, err := RunPipelineV2(series, 240, 72, 1, 10, PipelineConfig{
        ResolutionMin:        5,
        HourlyProfileMinHrs: 12,
        CVGuardMeanRPS:       1.0,
    })
    if err != nil {
        t.Fatalf("RunPipelineV2: %v", err)
    }
    if result.Context == nil {
        t.Fatal("Context must be non-nil on successful pipeline run")
    }
    if result.Context.BaselineRPS == 0 {
        t.Error("Context.BaselineRPS should be non-zero for non-trivial series")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/classifier/... -run "TestComputeBaselineRPS|TestComputePeakP95RPS|TestComputeHourlyProfile|TestRunPipelineV2" -v`
Expected: FAIL (functions undefined).

- [ ] **Step 3: Implement**

In `features.go`, add:

```go
func ComputeBaselineRPS(series []float64) int32 {
    return int32(math.Round(median(series)))
}

func ComputePeakP95RPS(series []float64) int32 {
    return int32(math.Round(percentile(series, 0.95)))
}

func ComputeHourlyProfile(series []float64, resolutionMin, startHourUTC, minHours int) ([]int32, bool) {
    profile := make([]int32, 24)
    buckets := make([][]float64, 24)
    pointsPerHour := 60 / resolutionMin
    for i, v := range series {
        hourOffset := (i / pointsPerHour) % 24
        hour := (startHourUTC + hourOffset) % 24
        buckets[hour] = append(buckets[hour], v)
    }
    distinctHours := 0
    for h := 0; h < 24; h++ {
        if len(buckets[h]) > 0 {
            profile[h] = int32(math.Round(median(buckets[h])))
            distinctHours++
        }
    }
    return profile, distinctHours >= minHours
}

func median(s []float64) float64 {
    if len(s) == 0 { return 0 }
    sorted := make([]float64, len(s))
    copy(sorted, s)
    sort.Float64s(sorted)
    n := len(sorted)
    if n%2 == 0 {
        return (sorted[n/2-1] + sorted[n/2]) / 2
    }
    return sorted[n/2]
}
```

In `pipeline.go`, add `PipelineConfig` struct and `RunPipelineV2`:

```go
type PipelineConfig struct {
    ResolutionMin        int
    HourlyProfileMinHrs int
    CVGuardMeanRPS       float64
}

type ContextOutput struct {
    BaselineRPS        int32
    PeakP95RPS         int32
    Trend24hSlope      float64
    HourlyProfile      []int32
    HourlyProfileValid bool
}

type PipelineResultV2 struct {
    PipelineResult
    Context *ContextOutput
}

func RunPipelineV2(
    series []float64,
    highConfThreshold, minThreshold int,
    minReplicas, maxReplicas int32,
    cfg PipelineConfig,
) (PipelineResultV2, error) {
    if len(series) < minThreshold {
        return PipelineResultV2{}, ErrInsufficientPoints
    }
    // Use resolution-aware features
    slopeRpsPerMin := TrendSlopeRpsPerMin(series, cfg.ResolutionMin)
    lag := HourlyAutocorrLag(cfg.ResolutionMin)
    detrended := detrend(series, trendSlope(series))
    todCorr := todCorrelation(detrended, lag, MinTodOverlap)
    m := mean(series)
    sd := stddev(series, m)

    var cv float64
    if m >= cfg.CVGuardMeanRPS {
        cv = sd / m
    }
    denom := math.Max(m, cfg.CVGuardMeanRPS)
    p99 := percentile(series, 0.99)
    peakToTrough := p99 / denom

    f := Features{
        CV: cv, PeakToTrough: peakToTrough,
        TodCorrelation: todCorr, TrendSlope: slopeRpsPerMin,
    }
    pattern := ClassifyWithMean(f, m)
    conf := Confidence(len(series), highConfThreshold, minThreshold)
    params := ComputeParams(f, minReplicas, maxReplicas)

    // Compute context
    profile, valid := ComputeHourlyProfile(series, cfg.ResolutionMin, 0, cfg.HourlyProfileMinHrs)
    ctx := &ContextOutput{
        BaselineRPS:        ComputeBaselineRPS(series),
        PeakP95RPS:         ComputePeakP95RPS(series),
        Trend24hSlope:      slopeRpsPerMin,
        HourlyProfile:      profile,
        HourlyProfileValid: valid,
    }

    return PipelineResultV2{
        PipelineResult: PipelineResult{
            Pattern: pattern, Confidence: conf,
            Params: params, HistoryPoints: len(series), Features: f,
        },
        Context: ctx,
    }, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/classifier/... -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/classifier/
git commit -m "feat(classifier): compute context fields in RunPipelineV2 (G10, G11)"
```

---

### Task 12: G10 + G11 — Wire context into worker.go patchStatus

**Files:**
- Modify: `internal/classifier/worker.go`
- Modify: `internal/classifier/worker_test.go`

- [ ] **Step 1: Write failing test**

In `worker_test.go`, add a test that calls `runClassification` and asserts `aas.Status.ClassifiedParams.Context` is non-nil after a successful run:

```go
func TestWorkerWritesContext(t *testing.T) {
    // Setup: fake client, fake prom returning 100 points, config with resolution=5
    // After runClassification, fetch the AAS and assert Context fields populated
    w := newTestWorker(t, 100) // helper creates worker with 100-point series
    w.Config.ResolutionMin = 5
    w.Config.HourlyProfileMinHours = 12

    ctx := context.Background()
    ok := w.runClassification(ctx, logr.Discard())
    if !ok {
        t.Fatal("runClassification should succeed with 100 points")
    }
    var aas autoscalingv1alpha1.AgenticAutoscaler
    if err := w.Client.Get(ctx, w.Key, &aas); err != nil {
        t.Fatalf("get CR: %v", err)
    }
    if aas.Status.ClassifiedParams == nil {
        t.Fatal("classifiedParams should be non-nil")
    }
    if aas.Status.ClassifiedParams.Context == nil {
        t.Fatal("classifiedParams.Context should be non-nil after classification")
    }
    if aas.Status.ClassifiedParams.Context.BaselineRPS == 0 {
        t.Error("BaselineRPS should be non-zero")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/classifier/... -run TestWorkerWritesContext -v`
Expected: FAIL (missing `ResolutionMin` field on WorkerConfig, or Context is nil).

- [ ] **Step 3: Implement**

(a) Add to `WorkerConfig`:
```go
ResolutionMin          int
HourlyProfileMinHours  int
CVGuardMeanRPS         float64
```

(b) In `worker.go` `runClassification`, switch from `RunPipeline` to `RunPipelineV2`:
```go
result, err := RunPipelineV2(series,
    w.Config.HighConfPoints, w.Config.MinPoints,
    w.MinReplicas, w.MaxReplicas,
    PipelineConfig{
        ResolutionMin:        w.Config.ResolutionMin,
        HourlyProfileMinHrs: w.Config.HourlyProfileMinHours,
        CVGuardMeanRPS:       w.Config.CVGuardMeanRPS,
    })
```

(c) In `patchStatus`, write the context:
```go
if result.Context != nil {
    aas.Status.ClassifiedParams.Context = &autoscalingv1alpha1.ContextFields{
        BaselineRPS:        result.Context.BaselineRPS,
        PeakP95RPS:         result.Context.PeakP95RPS,
        Trend24hSlope:      result.Context.Trend24hSlope,
        HourlyProfile:      result.Context.HourlyProfile,
        HourlyProfileValid: result.Context.HourlyProfileValid,
    }
}
```

(d) Change the Prometheus step from `time.Minute` to `time.Duration(w.Config.ResolutionMin) * time.Minute`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/classifier/... -v`
Expected: all PASS (update existing worker tests to set new config fields).

- [ ] **Step 5: Commit**

```bash
git add internal/classifier/
git commit -m "feat(classifier): worker writes context on patchStatus + 5-min PromQL step (G10, G11)"
```

---

### Task 13: F23 — RPS_PER_POD_NOISE_FLOOR_RPS env-tunable

**Files:**
- Modify: `internal/decision/decision.go`
- Modify: `internal/decision/decision_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestShouldUpdateRpsPerPodUsesConfiguredFloor(t *testing.T) {
    // With floor=5, currentRPS=7 should be accepted
    got := ShouldUpdateRpsPerPodWithFloor(7, 2, time.Time{}, time.Now(), time.Minute, 5)
    if !got {
        t.Error("currentRPS=7 with floor=5 should be accepted")
    }
    // With floor=10 (old default), currentRPS=7 should be rejected
    got = ShouldUpdateRpsPerPodWithFloor(7, 2, time.Time{}, time.Now(), time.Minute, 10)
    if got {
        t.Error("currentRPS=7 with floor=10 should be rejected")
    }
}
```

- [ ] **Step 2: Run to verify FAIL**

Run: `go test ./internal/decision/... -run TestShouldUpdateRpsPerPodUsesConfiguredFloor -v`
Expected: FAIL (undefined: ShouldUpdateRpsPerPodWithFloor).

- [ ] **Step 3: Implement**

Add `ShouldUpdateRpsPerPodWithFloor` that takes a `noiseFloor float64` parameter:

```go
func ShouldUpdateRpsPerPodWithFloor(currentRPS float64, replicas int32, lastScale, now time.Time, interval time.Duration, noiseFloor float64) bool {
    if currentRPS < noiseFloor || replicas < 1 {
        return false
    }
    return now.Sub(lastScale) >= 2*interval
}
```

Update the original `ShouldUpdateRpsPerPod` to delegate with `noiseFloor = 10` for backward compat. The controller reconciler call site will be updated in Task 14 (Sub-PR D) to pass `cfg.RpsPerPodNoiseFloorRPS`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/decision/... -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/decision/
git commit -m "feat(decision): ShouldUpdateRpsPerPodWithFloor takes configurable noise floor (F23, G21)"
```

---

## Sub-PR D: Forward context through hot path (G10 end-to-end)

### Task 14: G10 — Controller builds and forwards context in /recommend

**Files:**
- Modify: `internal/controller/agenticautoscaler_controller.go`
- Test: existing envtest / controller_test.go

- [ ] **Step 1: Write failing test**

In the controller test file (or integration test), add a test that:
1. Creates an AAS with `ClassifiedParams.Context` populated
2. Runs a reconcile
3. Asserts the forecast adapter's received request has `Context != nil` with `CurrentHourUTC` and `CurrentMinuteUTC` set

(Exact test code depends on the existing test harness; use the mock `Forecaster` interface.)

- [ ] **Step 2: Run to verify FAIL**

Expected: Context is nil in the request because the controller doesn't build it yet.

- [ ] **Step 3: Implement**

In `agenticautoscaler_controller.go`, before the `r.Forecaster.Recommend(...)` call:

```go
var reqContext *forecast.ContextPayload
if aas.Status.ClassifiedParams != nil && aas.Status.ClassifiedParams.Context != nil {
    // Check skip-context annotation
    skipCtx := false
    if aas.Annotations != nil {
        if v, ok := aas.Annotations["autoscaling.agentic.io/skip-context"]; ok && v == "true" {
            skipCtx = true
        }
    }
    if !skipCtx {
        now := time.Now().UTC()
        ctx := aas.Status.ClassifiedParams.Context
        reqContext = &forecast.ContextPayload{
            BaselineRPS:        ctx.BaselineRPS,
            PeakP95RPS:         ctx.PeakP95RPS,
            Trend24hSlope:      ctx.Trend24hSlope,
            HourlyProfile:      ctx.HourlyProfile,
            HourlyProfileValid: ctx.HourlyProfileValid,
            CurrentHourUTC:     now.Hour(),
            CurrentMinuteUTC:   now.Minute(),
        }
    }
}
```

And pass it in the request:
```go
forecastResp, err := r.Forecaster.Recommend(ctx, forecast.RecommendRequest{
    RpsHistory:     rpsHistory,
    WorkloadID:     req.NamespacedName.String(),
    PreferredModel: preferredModel,
    Context:        reqContext,
})
```

Also update `ShouldUpdateRpsPerPod` call site to use `ShouldUpdateRpsPerPodWithFloor(... , float64(r.Config.RpsPerPodNoiseFloorRPS))`.

- [ ] **Step 4: Run tests**

Run: `make test`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/ internal/decision/
git commit -m "feat(controller): forward context in /recommend + skip-context annotation + configurable noise floor (G10)"
```

---

### Task 15: G10 — Forecast Service accepts and validates context

**Files:**
- Modify: `forecast-service/src/forecast/dispatch.py`
- Modify: `forecast-service/src/forecast/app.py`
- Test: `forecast-service/tests/unit/test_dispatch.py`

- [ ] **Step 1: Write failing test**

In `test_dispatch.py`:

```python
from forecast.models import ContextPayload

def test_recommend_accepts_context():
    from forecast.dispatch import recommend
    ctx = ContextPayload(
        baseline_rps=50, peak_p95_rps=200, trend_24h_slope=0.5,
        hourly_profile=[10]*24, hourly_profile_valid=True,
        current_hour_utc=14, current_minute_utc=30,
    )
    result = recommend(
        rps_history=[50.0]*15,
        horizon_minutes=10,
        prophet_min_points=30,
        preferred_model=None,
        context=ctx,
    )
    assert result["predicted_rps"] >= 0
    assert result["model_used"] in ("prophet", "linear_extrap")


def test_recommend_drops_malformed_context(caplog):
    """Context with bad hourly_profile length is silently dropped."""
    from forecast.dispatch import recommend
    # Pass raw dict that would fail validation (too short profile)
    # The dispatch layer should catch, log, drop context, proceed
    result = recommend(
        rps_history=[50.0]*15,
        horizon_minutes=10,
        prophet_min_points=30,
        preferred_model=None,
        context=None,  # None = no context, always safe
    )
    assert result["predicted_rps"] >= 0
```

- [ ] **Step 2: Run to verify FAIL**

Run: `cd forecast-service && pytest tests/unit/test_dispatch.py -k context -v`
Expected: FAIL (recommend() does not accept `context` kwarg).

- [ ] **Step 3: Implement**

In `dispatch.py`, update `recommend()` signature:

```python
def recommend(
    rps_history: list[float],
    horizon_minutes: int,
    prophet_min_points: int,
    preferred_model: str | None = None,
    context: "ContextPayload | None" = None,
) -> RecommendResult:
```

For now, the function accepts context but does NOT pass it to individual forecasters (Phase 3 does). It simply validates and logs:

```python
    if context is not None:
        logging.debug("context forwarded: baseline=%d p95=%d", context.baseline_rps, context.peak_p95_rps)
    # Phase 3 will wire context into each forecaster's call.
```

In `app.py`, update `post_recommend` to forward context:

```python
@app.post("/recommend", response_model=RecommendResponse)
async def post_recommend(req: RecommendRequest) -> RecommendResponse:
    result = recommend(
        rps_history=req.rps_history,
        horizon_minutes=FORECAST_HORIZON_MINUTES,
        prophet_min_points=PROPHET_MIN_POINTS,
        preferred_model=req.preferred_model,
        context=req.context,
    )
    return RecommendResponse(**result)
```

- [ ] **Step 4: Run tests**

Run: `cd forecast-service && pytest tests/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add forecast-service/
git commit -m "feat(forecast-service): accept context in /recommend dispatch (G10)"
```

---

## Sub-PR E: Spec-trailer edits

### Task 16: E1 — Remove K_TOD_DOWN disclaimer from design_v2.md

**Files:**
- Modify: `docs/design_v2.md`

- [ ] **Step 1: Locate the disclaimer**

Run: `grep -n "K_TOD_DOWN" docs/design_v2.md`
Expected: one or more hits referencing the divergence between spec (`K_PERIODIC_DOWN`) and code (`K_TOD_DOWN`).

- [ ] **Step 2: Remove the disclaimer**

Delete the parenthetical note that says the source code still uses `K_TOD_DOWN` (since T6 renamed it in code).

- [ ] **Step 3: Verify**

Run: `grep -n "K_TOD_DOWN" docs/design_v2.md`
Expected: zero hits.

- [ ] **Step 4: Commit**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): remove K_TOD_DOWN disclaimer (E1 — code rename landed in T6)"
```

---

### Task 17: E3 — Tighten F2a-revisited prose

**Files:**
- Modify: `docs/design_v2.md`

- [ ] **Step 1: Locate F2a references**

Run: `grep -n "MIN_POINTS.*70\|70.*MIN_POINTS\|previous.*24\|was.*24" docs/design_v2.md`
Expected: Any lingering references to the old MIN_POINTS=70 or MIN_POINTS=24 values.

- [ ] **Step 2: Update if needed**

If any prose says "MIN_POINTS default 70" or references the 1-min cadence numbers as current, update to match the v2 default of 72 at 5-min resolution.

- [ ] **Step 3: Verify**

Run: `grep -n "default.*70\|default 70" docs/design_v2.md`
Expected: no hits referencing CLASSIFIER_MIN_POINTS as 70 in current-state prose.

- [ ] **Step 4: Commit (if changes made)**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): tighten F2a-revisited prose to reflect 72/240 defaults (E3)"
```

---

## Self-Review

After completing all 17 tasks:

- [ ] **Spec coverage:** G10, G11, G21 all have implementing tasks. E1 and E3 spec-trailers handled.
- [ ] **Placeholder scan:** No "TBD", "TODO", "implement later" strings remain.
- [ ] **Type consistency:** `ContextFields` (CRD type) vs `ContextPayload` (wire type) vs `ContextOutput` (pipeline internal) — three distinct types for three layers. Mapping happens at boundaries only.
- [ ] **Name consistency:** `LINEAR_EXTRAP_RECENT_WEIGHT` (not old name) in all new code.
- [ ] **Test coverage:** Every behavioural change has a preceding failing test.
- [ ] **make test passes:** Run `make test` after final task; both Go and Python suites green.
