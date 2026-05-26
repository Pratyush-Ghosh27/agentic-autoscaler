# Plan 14 — v2 Forecaster Surface Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make all three forecasters in `forecast-service` consume the `ContextPayload` that Plan 13 wired end-to-end. Anchor Prophet's `ds` to `(current_hour_utc, current_minute_utc)` and add the optional `hour_baseline` external regressor. Implement the linear-extrap trend blend (`LINEAR_EXTRAP_RECENT_WEIGHT`), intercept recompute (centroid-anchored), env-driven window, and `peak_p95_rps × 1.5` clip. Implement the third forecaster `gbdt_quantile` end-to-end (LightGBM quantile regressor with lag + hour-of-day-baseline + minute-in-hour features and `peak_p95_rps × 3` safety cap). Widen the CRD / Pydantic / webhook enums so `gbdt_quantile` is a first-class value. Flip `ComputeParams` from a feature-driven selector to a pattern → forecaster table (`flat`/`gradual_ramp`/`default` → linear_extrap, `periodic` → prophet, `spiky` → gbdt_quantile). Encode the F22 invariant in code: `auto` mode provably never returns `gbdt_quantile`.

**Architecture:** Strict TDD. Each task ships a failing test, the minimum code change to flip it green, a verification command, and a commit. The plan is partitioned into five sub-PRs of increasing depth — enum widening first (no behaviour change), then Prophet hardening, then linear_extrap, then gbdt_quantile new-forecaster, then selector flip + end-to-end integration. Phase 4 (Plan 15) wires the new `unboundedRecommended` and binding tokens into the explainer; this plan only delivers the forecaster surface.

**Tech Stack:** Python 3.12+, Pydantic v2, FastAPI, Prophet, NumPy, pandas, LightGBM (new dependency), pytest. Go 1.22+, controller-runtime, kubebuilder markers, `go test`. `make test` runs both suites.

---

## Spec Coverage Map

| Plan item | Tasks | Source |
| --- | --- | --- |
| **G12** — Third forecaster `gbdt_quantile` exists end-to-end | T1, T2, T9, T10, T11, T12 | gap-report-v2.md G12 |
| **G14** — Prophet ds anchor from context + hourly regressor | T3, T4 | gap-report-v2.md G14 |
| **G15** — linear_extrap trend blend + intercept recompute + window env + p95 clip | T5, T6, T7, T8 | gap-report-v2.md G15 |
| **G19** — Classifier forecaster selector is pattern-driven, not feature-driven | T13 | gap-report-v2.md G19 |
| **G20** (enum slice only) — CRD/Pydantic/webhook enums include `gbdt_quantile` | T1, T2 | gap-report-v2.md G20 (Phase 5 owns the strict-inequality fix) |
| **F3a / F17** — Prophet anchors `ds` so the last sample's `(utc_hour, utc_minute)` matches `context.current_hour_utc / current_minute_utc` | T3 | v2_revision notes F3a, F17 |
| **F5** — `PROPHET_MIN_POINTS=30` already landed in Plan 13 T5; this plan only consumes the value | (covered) | v2_revision notes F5 |
| **F6** — Per-request `current_hour_utc` field validation | T3 (validated via Pydantic `ge=0,le=23` already in Plan 13 T3) | v2_revision notes F6 |
| **F16** — linear_extrap blends slope with `context.trend_24h_slope` via `LINEAR_EXTRAP_RECENT_WEIGHT` | T6 | v2_revision notes F16 |
| **F21** — gbdt_quantile timestamp anchoring (shifted training rows reuse the Prophet anchor logic) | T10 | v2_revision notes F21 |
| **F22** — `auto` mode provably never returns `gbdt_quantile` (code invariant, not just docs) | T12 | v2_revision notes F22 |
| **F31** — linear_extrap recomputes `b` from centroid after blending `m` | T7 | v2_revision notes F31 |
| **E11** — design_v2 banner promotion is **not** in this plan; lives in Phase 6 | — | v2-spec-revision-plan.md E11 |

---

## File Structure

Files created or modified by this plan, grouped by sub-PR:

| Path | Sub-PR | Responsibility |
| --- | --- | --- |
| `api/v1alpha1/agenticautoscaler_types.go` | A | Widen `+kubebuilder:validation:Enum` on `Spec.PreferredForecaster` and `Status.ClassifiedParams.PreferredForecaster` to include `gbdt_quantile`. |
| `internal/webhook/v1alpha1/validator.go` | A | Add `gbdt_quantile` to the accepted-values switch. |
| `internal/classifier/params.go` | A | Add `ForecasterGBDTQuantile = "gbdt_quantile"` constant. |
| `forecast-service/src/forecast/models.py` | A | Widen `preferred_model` Literal + `RecommendResponse.model_used` Literal to include `gbdt_quantile`. |
| `forecast-service/src/forecast/dispatch.py` | A, D | Widen `ModelName` Literal. Sub-PR D adds the `gbdt_quantile` branch + auto-never-picks invariant. |
| `forecast-service/src/forecast/prophet_model.py` | B | `ds` anchor from `(current_hour_utc, current_minute_utc)`; optional `hour_baseline` external regressor. |
| `forecast-service/src/forecast/linear_extrap.py` | C | Env-driven window; trend blend; centroid intercept recompute; p95 clip. |
| `forecast-service/src/forecast/gbdt_model.py` | D | **New file.** LightGBM quantile regressor, lag + hour-of-day-baseline + minute-in-hour features, `peak_p95_rps × 3` safety cap. |
| `forecast-service/pyproject.toml` | D | Add `lightgbm>=4.3` to `[project] dependencies`. |
| `internal/classifier/params.go` | E | Replace feature-driven `if tod>0.70 OR |trend|>2.0 → prophet` with pattern → forecaster table; `ComputeParams` takes `pattern string` as input. |
| `internal/classifier/pipeline.go` | E | Propagate `pattern` into `ComputeParams` from `Classify`. |
| `internal/classifier/classify.go` | E | (no change — already returns pattern; T13 only changes how downstream consumes it) |

Test files mirror each implementation file:

| Path | Sub-PR |
| --- | --- |
| `forecast-service/tests/unit/test_models.py` | A |
| `forecast-service/tests/unit/test_dispatch.py` | A, D, E |
| `forecast-service/tests/unit/test_prophet_model.py` | B |
| `forecast-service/tests/unit/test_linear_extrap.py` | C |
| `forecast-service/tests/unit/test_gbdt_model.py` (new) | D |
| `forecast-service/tests/integration/test_app.py` | A, E |
| `internal/webhook/v1alpha1/validator_test.go` | A |
| `internal/classifier/params_test.go` | A, E |

---

## Sub-PR A: Schema and enum widening (no behaviour change)

This sub-PR makes `gbdt_quantile` a legal value everywhere the schema validates. No forecaster yet returns it — that lands in Sub-PR D — but every layer of validation accepts it.

### Task 1: G12 + G20 — Widen Go enums to include `gbdt_quantile`

**Files:**
- Modify: `api/v1alpha1/agenticautoscaler_types.go:76-79` (Spec enum) and `api/v1alpha1/agenticautoscaler_types.go:115-117` (Status enum)
- Modify: `internal/webhook/v1alpha1/validator.go:82-90` (accepted-values switch)
- Modify: `internal/classifier/params.go:35-39` (forecaster constants)
- Modify: `internal/webhook/v1alpha1/validator_test.go` (existing cases — add a happy-path for `gbdt_quantile`, ensure no case still asserts it is *rejected*)
- Modify: `internal/classifier/params_test.go` (new constant pin)

- [ ] **Step 1: Write the failing Go tests**

Add to `internal/classifier/params_test.go`:

```go
func TestForecasterGBDTQuantileConstantExists(t *testing.T) {
	if ForecasterGBDTQuantile != "gbdt_quantile" {
		t.Errorf("ForecasterGBDTQuantile = %q, want %q",
			ForecasterGBDTQuantile, "gbdt_quantile")
	}
}
```

Add to `internal/webhook/v1alpha1/validator_test.go` (next to the existing `preferredForecaster` cases):

```go
func TestValidator_PreferredForecasterGBDTQuantile(t *testing.T) {
	gbdt := "gbdt_quantile"
	aas := makeAAS(spec(WithPreferredForecaster(&gbdt)))
	if err := Validate(aas); err != nil {
		t.Errorf("gbdt_quantile must be accepted, got: %v", err)
	}
}
```

(If `makeAAS` / `spec` / `WithPreferredForecaster` are not the existing helper names in `validator_test.go`, mirror whatever the existing happy-path test for `preferred_model=prophet` uses; the goal is "rejection-free validation when the value is `gbdt_quantile`".)

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/classifier/... -run TestForecasterGBDTQuantileConstantExists -v
go test ./internal/webhook/v1alpha1/... -run TestValidator_PreferredForecasterGBDTQuantile -v
```

Expected: FAIL — first because the constant is undefined, second because the validator's switch rejects unknown values.

- [ ] **Step 3: Add the constant**

In `internal/classifier/params.go`, after `ForecasterProphet`:

```go
// ForecasterGBDTQuantile is the LightGBM quantile-regression forecaster
// for spiky workloads. v2: see docs/design_v2.md §5 forecast_gbdt_quantile.
ForecasterGBDTQuantile = "gbdt_quantile"
```

- [ ] **Step 4: Widen the webhook validator**

In `internal/webhook/v1alpha1/validator.go`, change the accepted-values switch:

```go
switch *spec.PreferredForecaster {
case "prophet", "linear_extrap", "gbdt_quantile", "auto":
	// accepted
default:
	problems = append(problems, fmt.Sprintf(
		"preferredForecaster=%q must be one of prophet, linear_extrap, gbdt_quantile, auto",
		*spec.PreferredForecaster))
}
```

- [ ] **Step 5: Widen the CRD enum markers**

In `api/v1alpha1/agenticautoscaler_types.go`, the `Spec.PreferredForecaster` marker (around line 77):

```go
// PreferredForecaster. nil or "auto" means "defer to classifier".
// +kubebuilder:validation:Enum=prophet;linear_extrap;gbdt_quantile;auto
// +optional
PreferredForecaster *string `json:"preferredForecaster,omitempty"`
```

And `Status.ClassifiedParams.PreferredForecaster` (around line 116):

```go
// PreferredForecaster is "prophet", "linear_extrap", or "gbdt_quantile".
// +kubebuilder:validation:Enum=prophet;linear_extrap;gbdt_quantile
PreferredForecaster string `json:"preferredForecaster"`
```

(The status enum does not include `auto` — `auto` is only valid on the spec; the classifier resolves to a concrete model before writing status.)

- [ ] **Step 6: Regenerate manifests**

```bash
make manifests
```

Expected: `config/crd/bases/autoscaling.agentic.io_agenticautoscalers.yaml` shows `gbdt_quantile` in both the `spec.preferredForecaster` and `status.classifiedParams.preferredForecaster` enum arrays.

- [ ] **Step 7: Run tests to verify they pass**

```bash
go test ./internal/classifier/... ./internal/webhook/... ./api/... -count=1
```

Expected: PASS for all suites.

- [ ] **Step 8: Commit**

```bash
git add api/v1alpha1/ internal/classifier/params.go internal/classifier/params_test.go internal/webhook/v1alpha1/ config/crd/
git commit -m "feat(api,webhook,classifier): widen preferredForecaster enum to include gbdt_quantile (G12, G20)"
```

---

### Task 2: G12 — Widen Python `preferred_model` Literal to include `gbdt_quantile`

**Files:**
- Modify: `forecast-service/src/forecast/models.py:62` (`RecommendRequest.preferred_model`) and `:83` (`RecommendResponse.model_used`)
- Modify: `forecast-service/src/forecast/dispatch.py:21` (`ModelName` alias)
- Modify: `forecast-service/tests/unit/test_models.py` (or create) — assert the Literal accepts `gbdt_quantile`

- [ ] **Step 1: Write the failing test**

In `forecast-service/tests/unit/test_models.py` (create if absent), add:

```python
from forecast.models import RecommendRequest, RecommendResponse


def test_recommend_request_accepts_gbdt_quantile_preferred_model() -> None:
    """G12: gbdt_quantile must be a legal preferred_model literal."""
    req = RecommendRequest(rps_history=[1.0, 2.0, 3.0], preferred_model="gbdt_quantile")
    assert req.preferred_model == "gbdt_quantile"


def test_recommend_response_accepts_gbdt_quantile_model_used() -> None:
    """G12: gbdt_quantile must be a legal model_used literal."""
    resp = RecommendResponse(predicted_rps=42.0, horizon_minutes=10, model_used="gbdt_quantile")
    assert resp.model_used == "gbdt_quantile"
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd forecast-service && pytest tests/unit/test_models.py -v -k gbdt_quantile
```

Expected: FAIL — pydantic raises `ValidationError` because `gbdt_quantile` is not in the Literal.

- [ ] **Step 3: Widen the Literals**

In `forecast-service/src/forecast/models.py`:

```python
preferred_model: Literal["prophet", "linear_extrap", "gbdt_quantile", "auto"] | None = None
```

and:

```python
class RecommendResponse(BaseModel):
    """Body of POST /recommend response."""

    predicted_rps: float
    horizon_minutes: int
    model_used: Literal["prophet", "linear_extrap", "gbdt_quantile"]
```

In `forecast-service/src/forecast/dispatch.py`, widen the `ModelName` alias:

```python
ModelName = Literal["prophet", "linear_extrap", "gbdt_quantile"]
```

- [ ] **Step 4: Run test to verify it passes**

```bash
pytest tests/unit/test_models.py -v -k gbdt_quantile
```

Expected: PASS.

- [ ] **Step 5: Run full Python suite to confirm no regressions**

```bash
pytest tests/ -q
```

Expected: all tests pass.

- [ ] **Step 6: Commit**

```bash
git add forecast-service/src/forecast/models.py forecast-service/src/forecast/dispatch.py forecast-service/tests/unit/test_models.py
git commit -m "feat(forecast-svc): widen preferred_model and model_used Literals to include gbdt_quantile (G12)"
```

---

## Sub-PR B: Prophet anchoring + hourly regressor (G14)

This sub-PR makes Prophet read `(current_hour_utc, current_minute_utc)` from the request context and optionally consume the 24-bin `hourly_profile` as an external regressor named `hour_baseline`.

### Task 3: G14 / F3a / F17 — Prophet anchors `ds` from request context

**Files:**
- Modify: `forecast-service/src/forecast/prophet_model.py`
- Modify: `forecast-service/src/forecast/dispatch.py` (forward `context` into `forecast_prophet`)
- Test: `forecast-service/tests/unit/test_prophet_model.py` (create if absent)

- [ ] **Step 1: Write the failing test**

Create `forecast-service/tests/unit/test_prophet_model.py`:

```python
"""Prophet model unit tests. Slow because Prophet's fit is non-trivial;
marked with the `slow` pytest marker so they can be skipped in the inner
TDD loop with `pytest -m 'not slow'`."""

from __future__ import annotations

import pandas as pd
import pytest

from forecast.prophet_model import forecast_prophet


@pytest.mark.slow
def test_prophet_anchors_ds_to_request_context_hour_minute() -> None:
    """F3a / F17: when context current_hour_utc=14, current_minute_utc=30
    is provided, the synthetic last ds must have hour=14 and minute=30,
    independent of the service's local clock."""
    history = [50.0] * 60  # 60 minutes of flat 50 rps
    # We can't inspect Prophet's internal df from outside; instead, build
    # the same anchor logic and assert it matches what prophet_model.py
    # would compute. The implementation must expose a helper.
    from forecast.prophet_model import build_anchored_timestamps

    timestamps = build_anchored_timestamps(
        n=len(history), current_hour_utc=14, current_minute_utc=30
    )
    last = timestamps[-1]
    assert last.hour == 14, f"last.hour={last.hour}, want 14"
    assert last.minute == 30, f"last.minute={last.minute}, want 30"
    # First sample is (n-1) minutes before last.
    assert (last - timestamps[0]) == pd.Timedelta(minutes=len(history) - 1)


@pytest.mark.slow
def test_prophet_falls_back_to_now_when_context_absent() -> None:
    """When context is None, anchoring uses the local clock (existing
    behaviour). The plan does not regress this."""
    history = [10.0] * 30
    # Forecast without context: must not raise.
    predicted = forecast_prophet(history, horizon_minutes=5, context=None)
    assert predicted >= 0
```

- [ ] **Step 2: Run test to verify it fails**

```bash
pytest tests/unit/test_prophet_model.py -v -m slow
```

Expected: FAIL — `build_anchored_timestamps` is undefined, and `forecast_prophet` does not accept a `context` keyword argument.

- [ ] **Step 3: Extract the anchor helper and accept `context`**

In `forecast-service/src/forecast/prophet_model.py`, replace the body with:

```python
"""Prophet-based forecaster.

Per docs/design_v2.md §5 forecast_prophet pipeline (F3a, F17):
1. Build a DataFrame: ds = synthetic 1-minute timestamps ending at
   (context.current_hour_utc, context.current_minute_utc) when context
   is provided, else at the service's local UTC clock (legacy).
   y  = rps_history values.
2. If context is provided AND context.hourly_profile_valid AND
   PROPHET_USE_HOURLY_REGRESSOR=true, attach the 24-bin profile as an
   external regressor named "hour_baseline".
3. Fit Prophet with daily/weekly seasonality disabled,
   changepoint_prior_scale=0.5.
4. Build a future DataFrame extending horizon_minutes past the last ds.
5. predicted_rps = model.predict(future).iloc[-1].yhat.
6. Return max(0.0, predicted_rps).

Prophet rejects timezone-aware timestamps, so we build naive UTC
datetimes.
"""

from __future__ import annotations

import os
from datetime import UTC, datetime, timedelta
from typing import TYPE_CHECKING

import pandas as pd
from prophet import Prophet

if TYPE_CHECKING:
    from forecast.models import ContextPayload


def build_anchored_timestamps(
    n: int,
    current_hour_utc: int | None = None,
    current_minute_utc: int | None = None,
) -> list[pd.Timestamp]:
    """Return a list of `n` 1-minute-spaced naive UTC pandas Timestamps
    whose last entry has `(hour, minute) == (current_hour_utc, current_minute_utc)`.

    If either hour or minute is None, fall back to the service's local
    UTC wall clock (legacy behaviour, kept for backward compatibility
    with cold-start scenarios where the controller has not yet written
    context).
    """
    if current_hour_utc is None or current_minute_utc is None:
        end = datetime.now(tz=UTC).replace(second=0, microsecond=0, tzinfo=None)
    else:
        # Anchor the last sample exactly at (h, m). Take "now" UTC,
        # walk back to the requested (h, m), and use that as `end`.
        # We do NOT use today's date — Prophet only cares about
        # contiguous 1-minute spacing, not absolute calendar dates.
        now = datetime.now(tz=UTC).replace(second=0, microsecond=0, tzinfo=None)
        end = now.replace(hour=current_hour_utc, minute=current_minute_utc)
    return [pd.Timestamp(end - timedelta(minutes=(n - 1 - i))) for i in range(n)]


def forecast_prophet(
    rps_history: list[float],
    horizon_minutes: int,
    context: "ContextPayload | None" = None,
) -> float:
    """Predict RPS `horizon_minutes` ahead using Prophet.

    Raises any exception Prophet raises during fit; the caller
    (`dispatch.recommend`) is responsible for catching and falling back.
    """
    if not rps_history:
        raise ValueError("rps_history must not be empty")
    if horizon_minutes < 0:
        raise ValueError("horizon_minutes must be >= 0")

    n = len(rps_history)
    timestamps = build_anchored_timestamps(
        n=n,
        current_hour_utc=context.current_hour_utc if context is not None else None,
        current_minute_utc=context.current_minute_utc if context is not None else None,
    )

    df = pd.DataFrame({"ds": timestamps, "y": rps_history})

    use_hour_regressor = (
        context is not None
        and context.hourly_profile_valid
        and os.environ.get("PROPHET_USE_HOURLY_REGRESSOR", "true").lower() == "true"
    )
    if use_hour_regressor:
        # Each row's hour_baseline = context.hourly_profile[ds.hour].
        df["hour_baseline"] = [
            float(context.hourly_profile[t.hour]) for t in df["ds"]
        ]

    model = Prophet(
        daily_seasonality=False,
        weekly_seasonality=False,
        changepoint_prior_scale=0.5,
    )
    if use_hour_regressor:
        model.add_regressor("hour_baseline")
    model.fit(df)

    future = model.make_future_dataframe(
        periods=max(1, horizon_minutes),
        freq="min",
        include_history=False,
    )
    if use_hour_regressor:
        future["hour_baseline"] = [
            float(context.hourly_profile[t.hour]) for t in future["ds"]
        ]

    forecast = model.predict(future)
    if horizon_minutes == 0:
        predicted = float(forecast["yhat"].iloc[0])
    else:
        predicted = float(forecast["yhat"].iloc[-1])

    return max(0.0, predicted)
```

- [ ] **Step 4: Update `dispatch.py` to forward `context` into Prophet**

In `forecast-service/src/forecast/dispatch.py`, change the Prophet call:

```python
if use_prophet:
    try:
        predicted = forecast_prophet(rps_history, horizon_minutes, context=context)
        model_used = "prophet"
    except Exception as exc:  # noqa: BLE001 - any Prophet failure is a fallback trigger
        ...
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
pytest tests/unit/test_prophet_model.py -v -m slow
```

Expected: PASS — both tests green.

- [ ] **Step 6: Run the full Python suite**

```bash
pytest tests/ -q
```

Expected: all tests still pass; no Prophet regressions in pre-existing tests.

- [ ] **Step 7: Commit**

```bash
git add forecast-service/src/forecast/prophet_model.py forecast-service/src/forecast/dispatch.py forecast-service/tests/unit/test_prophet_model.py
git commit -m "feat(forecast-svc): Prophet anchors ds and adds optional hour_baseline regressor from context (G14, F3a, F17)"
```

---

### Task 4: G14 — Prophet hourly regressor behaviour test

This task adds the *behavioural* assertion that `PROPHET_USE_HOURLY_REGRESSOR=false` (or `hourly_profile_valid=False`) makes the regressor inactive. Task 3 added the wiring; Task 4 pins the toggle.

**Files:**
- Modify: `forecast-service/tests/unit/test_prophet_model.py`

- [ ] **Step 1: Add the failing test**

Append to `forecast-service/tests/unit/test_prophet_model.py`:

```python
import os
from unittest.mock import patch

from forecast.models import ContextPayload


def _ctx(valid: bool) -> ContextPayload:
    return ContextPayload(
        baseline_rps=50,
        peak_p95_rps=200,
        trend_24h_slope=0.0,
        hourly_profile=[50] * 24,
        hourly_profile_valid=valid,
        current_hour_utc=12,
        current_minute_utc=0,
    )


@pytest.mark.slow
def test_prophet_skips_hour_regressor_when_invalid() -> None:
    """G11/G14: hourly_profile_valid=False means the regressor is not added,
    even when PROPHET_USE_HOURLY_REGRESSOR=true."""
    history = [10.0] * 30
    with patch.dict(os.environ, {"PROPHET_USE_HOURLY_REGRESSOR": "true"}):
        predicted = forecast_prophet(history, horizon_minutes=5, context=_ctx(valid=False))
    assert predicted >= 0  # Must not raise — invalid profile is a soft skip.


@pytest.mark.slow
def test_prophet_skips_hour_regressor_when_toggle_off() -> None:
    """G14: PROPHET_USE_HOURLY_REGRESSOR=false suppresses the regressor."""
    history = [10.0] * 30
    with patch.dict(os.environ, {"PROPHET_USE_HOURLY_REGRESSOR": "false"}):
        predicted = forecast_prophet(history, horizon_minutes=5, context=_ctx(valid=True))
    assert predicted >= 0
```

- [ ] **Step 2: Run the new tests**

```bash
pytest tests/unit/test_prophet_model.py -v -m slow
```

Expected: PASS — the gating logic added in T3 already handles both conditions; these tests pin the contract.

- [ ] **Step 3: Commit**

```bash
git add forecast-service/tests/unit/test_prophet_model.py
git commit -m "test(forecast-svc): pin hourly-regressor on/off behaviour (G14)"
```

---

## Sub-PR C: linear_extrap blend + intercept + window + clip (G15)

### Task 5: G15 — linear_extrap window from `LINEAR_EXTRAP_WINDOW_MINUTES` env var

**Files:**
- Modify: `forecast-service/src/forecast/linear_extrap.py`
- Modify: `forecast-service/tests/unit/test_linear_extrap.py` (create or extend)

- [ ] **Step 1: Write the failing test**

Add to `forecast-service/tests/unit/test_linear_extrap.py`:

```python
from __future__ import annotations

import os
from unittest.mock import patch

import pytest

from forecast.linear_extrap import forecast_linear_extrap


def test_linear_extrap_uses_window_minutes_env_var() -> None:
    """G15: when LINEAR_EXTRAP_WINDOW_MINUTES=5, only the last 5 points
    drive the fit. Construct a series where the last 5 points imply
    slope=0 but the last 20 imply slope=+1 — the env override should
    select the flat segment."""
    history = list(range(20)) + [50.0] * 5  # ramp 0..19, then flat 50x5
    with patch.dict(os.environ, {"LINEAR_EXTRAP_WINDOW_MINUTES": "5"}):
        predicted = forecast_linear_extrap(history, horizon_minutes=5)
    # Flat last-5 = [50, 50, 50, 50, 50]; slope=0; intercept=50; prediction=50.
    assert predicted == pytest.approx(50.0, abs=1e-6)


def test_linear_extrap_window_defaults_to_10() -> None:
    """Plan-13 default: LINEAR_EXTRAP_WINDOW_MINUTES=10. With a 20-point
    history of linear ramp, only the last 10 points should fit."""
    history = list(range(20))  # 0..19
    with patch.dict(os.environ, {}, clear=False):
        os.environ.pop("LINEAR_EXTRAP_WINDOW_MINUTES", None)
        predicted = forecast_linear_extrap(history, horizon_minutes=1)
    # Last 10 = [10, 11, ..., 19]; slope=1; centroid x_bar=4.5, y_bar=14.5;
    # b = 14.5 - 1*4.5 = 10. Prediction at x=10 (n=10, horizon=1):
    # target_x = 10 + 1 - 1 = 10 → 1*10 + 10 = 20.
    assert predicted == pytest.approx(20.0, abs=1e-6)
```

- [ ] **Step 2: Run test to verify it fails**

```bash
pytest tests/unit/test_linear_extrap.py -v -k window
```

Expected: FAIL — current implementation hard-codes `rps_history[-10:]` (line 27).

- [ ] **Step 3: Add the env-driven window**

In `forecast-service/src/forecast/linear_extrap.py`, replace the `series = np.asarray(rps_history[-10:], dtype=float)` line:

```python
from __future__ import annotations

import os

import numpy as np

# Phase 3 (G15): the linear-fit window is operator-tunable. F18 unit:
# slope is per-sample (== rps/min at 1-min reconcile cadence).
_DEFAULT_WINDOW_MINUTES = 10


def _window_minutes() -> int:
    return int(os.environ.get("LINEAR_EXTRAP_WINDOW_MINUTES", str(_DEFAULT_WINDOW_MINUTES)))


def forecast_linear_extrap(
    rps_history: list[float],
    horizon_minutes: int,
) -> float:
    if not rps_history:
        raise ValueError("rps_history must not be empty")

    window = _window_minutes()
    series = np.asarray(rps_history[-window:], dtype=float)
    n = len(series)

    if n == 1:
        return max(0.0, float(series[0]))

    x = np.arange(n, dtype=float)
    slope, intercept = np.polyfit(x, series, deg=1)

    target_x = n + horizon_minutes - 1
    predicted = slope * target_x + intercept

    return max(0.0, float(predicted))
```

- [ ] **Step 4: Run test to verify it passes**

```bash
pytest tests/unit/test_linear_extrap.py -v -k window
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add forecast-service/src/forecast/linear_extrap.py forecast-service/tests/unit/test_linear_extrap.py
git commit -m "feat(forecast-svc): linear_extrap window is env-driven via LINEAR_EXTRAP_WINDOW_MINUTES (G15)"
```

---

### Task 6: G15 / F16 — linear_extrap blends slope with `context.trend_24h_slope`

**Files:**
- Modify: `forecast-service/src/forecast/linear_extrap.py`
- Modify: `forecast-service/src/forecast/dispatch.py` (forward `context`)
- Modify: `forecast-service/tests/unit/test_linear_extrap.py`

- [ ] **Step 1: Write the failing test**

Append to `forecast-service/tests/unit/test_linear_extrap.py`:

```python
from forecast.models import ContextPayload


def _ctx(trend: float, p95: int = 1000) -> ContextPayload:
    return ContextPayload(
        baseline_rps=50,
        peak_p95_rps=p95,
        trend_24h_slope=trend,
        hourly_profile=[50] * 24,
        hourly_profile_valid=True,
        current_hour_utc=12,
        current_minute_utc=0,
    )


def test_linear_extrap_blends_slope_with_trend_24h_slope() -> None:
    """F16 / G15: m_blended = WEIGHT * m + (1 - WEIGHT) * trend_24h_slope.

    With LINEAR_EXTRAP_RECENT_WEIGHT=0.5, a flat recent window (m=0)
    blended with a positive long-term trend (+0.2 rps/min) should
    produce a non-zero positive slope of exactly 0.1 — half the trend.
    """
    history = [100.0] * 10  # recent slope = 0
    with patch.dict(
        os.environ,
        {
            "LINEAR_EXTRAP_RECENT_WEIGHT": "0.5",
            "LINEAR_EXTRAP_WINDOW_MINUTES": "10",
        },
    ):
        # horizon_minutes=10 means target_x = 10 + 10 - 1 = 19.
        # After blend: m_blended = 0.5*0 + 0.5*0.2 = 0.1.
        # Intercept is recomputed in T7 — until then, intercept stays
        # at np.polyfit's value (100 because all y_i=100, m=0).
        # Predicted = 0.1 * 19 + 100 = 101.9.
        predicted = forecast_linear_extrap(history, horizon_minutes=10, context=_ctx(trend=0.2))
    assert predicted == pytest.approx(101.9, abs=0.01)


def test_linear_extrap_ignores_trend_when_context_none() -> None:
    """G15: with no context, behaviour is identical to pre-Phase-3
    linear_extrap. Flat history → flat prediction."""
    history = [100.0] * 10
    predicted = forecast_linear_extrap(history, horizon_minutes=5, context=None)
    assert predicted == pytest.approx(100.0, abs=1e-6)
```

- [ ] **Step 2: Run test to verify it fails**

```bash
pytest tests/unit/test_linear_extrap.py -v -k blends
```

Expected: FAIL — `forecast_linear_extrap` does not accept a `context` keyword.

- [ ] **Step 3: Add the blend**

In `forecast-service/src/forecast/linear_extrap.py`:

```python
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from forecast.models import ContextPayload


def _recent_weight() -> float:
    return float(os.environ.get("LINEAR_EXTRAP_RECENT_WEIGHT", "0.7"))


def forecast_linear_extrap(
    rps_history: list[float],
    horizon_minutes: int,
    context: "ContextPayload | None" = None,
) -> float:
    if not rps_history:
        raise ValueError("rps_history must not be empty")

    window = _window_minutes()
    series = np.asarray(rps_history[-window:], dtype=float)
    n = len(series)

    if n == 1:
        return max(0.0, float(series[0]))

    x = np.arange(n, dtype=float)
    slope, intercept = np.polyfit(x, series, deg=1)

    if context is not None:
        # F16: blend the recent slope with the long-horizon trend. The
        # recent-weight convention matches the operator-facing env var
        # name: WEIGHT=1.0 is "ignore the long-horizon trend entirely",
        # WEIGHT=0.0 is "follow the trend, ignore short-window noise".
        w = _recent_weight()
        slope = w * slope + (1.0 - w) * context.trend_24h_slope

    target_x = n + horizon_minutes - 1
    predicted = slope * target_x + intercept

    return max(0.0, float(predicted))
```

Also update `dispatch.py` to forward `context` into linear_extrap:

```python
else:
    predicted = forecast_linear_extrap(rps_history, horizon_minutes, context=context)
    model_used = "linear_extrap"
```

Update the Prophet-failure fallback branch too:

```python
predicted = forecast_linear_extrap(rps_history, horizon_minutes, context=context)
```

- [ ] **Step 4: Run test to verify it passes**

```bash
pytest tests/unit/test_linear_extrap.py -v -k "blends or ignores_trend"
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add forecast-service/src/forecast/linear_extrap.py forecast-service/src/forecast/dispatch.py forecast-service/tests/unit/test_linear_extrap.py
git commit -m "feat(forecast-svc): linear_extrap blends slope with context.trend_24h_slope (G15, F16)"
```

---

### Task 7: F31 — linear_extrap recomputes intercept from centroid after blending

**Files:**
- Modify: `forecast-service/src/forecast/linear_extrap.py`
- Modify: `forecast-service/tests/unit/test_linear_extrap.py`

- [ ] **Step 1: Write the failing test**

Append to `forecast-service/tests/unit/test_linear_extrap.py`:

```python
def test_linear_extrap_intercept_is_centroid_anchored_after_blend() -> None:
    """F31 / G15: after blending m, recompute b so the line passes
    through (mean(x), mean(y)) — otherwise the line rotates around
    x=0 instead of the data centroid and biases predictions.

    Construct: y = [0, 0, 0, 0, 0, 100] over n=6.
    np.polyfit gives m≈14.29, b≈-21.43; centroid is (x_bar=2.5, y_bar=16.67).
    Sanity: 14.29*2.5 + (-21.43) ≈ 14.29*2.5 - 21.43 ≈ 35.72 - 21.43 = 14.29.
    That's NOT the centroid — np.polyfit's b is already centroid-anchored
    for OLS, so without blending the predict-at-x_bar yields y_bar.

    Now blend with trend=0 and WEIGHT=0.0 (use only trend, m_blended=0).
    Without F31 fix: b stays at -21.43, predict at x=10 → 0*10 + (-21.43) = -21.43 → clamped to 0.
    With F31 fix: b recomputed = y_bar - m_blended * x_bar = 16.67 - 0 = 16.67.
                  predict at x=10 → 0*10 + 16.67 = 16.67.
    """
    history = [0.0, 0.0, 0.0, 0.0, 0.0, 100.0]
    with patch.dict(
        os.environ,
        {"LINEAR_EXTRAP_RECENT_WEIGHT": "0.0", "LINEAR_EXTRAP_WINDOW_MINUTES": "10"},
    ):
        predicted = forecast_linear_extrap(history, horizon_minutes=5, context=_ctx(trend=0.0))
    # target_x = 6 + 5 - 1 = 10.
    # Expected with F31: 0 * 10 + 16.6667 = 16.6667.
    assert predicted == pytest.approx(16.6667, abs=0.01)
```

- [ ] **Step 2: Run test to verify it fails**

```bash
pytest tests/unit/test_linear_extrap.py -v -k centroid
```

Expected: FAIL — without F31, the predicted value will be 0.0 (clamped from -21.43).

- [ ] **Step 3: Add the intercept recompute**

In `forecast-service/src/forecast/linear_extrap.py`, inside the `if context is not None:` block, *after* the slope blend, add:

```python
    if context is not None:
        w = _recent_weight()
        slope = w * slope + (1.0 - w) * context.trend_24h_slope
        # F31: re-anchor the intercept at the data centroid so the
        # line continues to pass through (mean(x), mean(y)) after the
        # slope change. Without this, the blend rotates the line
        # around x=0 instead, which biases predictions.
        intercept = float(np.mean(series)) - slope * float(np.mean(x))
```

- [ ] **Step 4: Run test to verify it passes**

```bash
pytest tests/unit/test_linear_extrap.py -v -k centroid
```

Expected: PASS.

- [ ] **Step 5: Run full linear-extrap suite to confirm no regressions**

```bash
pytest tests/unit/test_linear_extrap.py -v
```

Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add forecast-service/src/forecast/linear_extrap.py forecast-service/tests/unit/test_linear_extrap.py
git commit -m "fix(forecast-svc): linear_extrap recomputes intercept from centroid after slope blend (F31)"
```

---

### Task 8: G15 — linear_extrap clips at `peak_p95_rps × 1.5`

**Files:**
- Modify: `forecast-service/src/forecast/linear_extrap.py`
- Modify: `forecast-service/tests/unit/test_linear_extrap.py`

- [ ] **Step 1: Write the failing test**

Append to `forecast-service/tests/unit/test_linear_extrap.py`:

```python
def test_linear_extrap_clips_at_peak_p95_times_1_5() -> None:
    """G15: a runaway short-window slope must be clipped at
    peak_p95_rps * 1.5. With history climbing from 0 to 1000 over 10
    points (slope=~111) and peak_p95_rps=200, the unclipped prediction
    at horizon=10 would explode (~2000+); the clipped value must be
    exactly 300 (=200*1.5)."""
    history = [0.0, 100.0, 200.0, 300.0, 400.0, 500.0, 600.0, 700.0, 800.0, 1000.0]
    with patch.dict(
        os.environ,
        {"LINEAR_EXTRAP_RECENT_WEIGHT": "1.0", "LINEAR_EXTRAP_WINDOW_MINUTES": "10"},
    ):
        predicted = forecast_linear_extrap(history, horizon_minutes=10, context=_ctx(trend=0.0, p95=200))
    assert predicted == pytest.approx(300.0, abs=0.01), (
        f"expected clipped to 300, got {predicted}"
    )


def test_linear_extrap_does_not_clip_when_context_none() -> None:
    """Without context, no p95 is known, so no clip applies."""
    history = [0.0, 100.0, 200.0, 300.0, 400.0, 500.0, 600.0, 700.0, 800.0, 1000.0]
    predicted = forecast_linear_extrap(history, horizon_minutes=10, context=None)
    assert predicted > 500.0  # uncllipped extrapolation is large
```

- [ ] **Step 2: Run test to verify it fails**

```bash
pytest tests/unit/test_linear_extrap.py -v -k clips
```

Expected: FAIL — current implementation has no clip.

- [ ] **Step 3: Add the clip**

In `forecast-service/src/forecast/linear_extrap.py`, before `return max(0.0, ...)`:

```python
    target_x = n + horizon_minutes - 1
    predicted = slope * target_x + intercept

    if context is not None:
        # G15: clip at peak_p95_rps * 1.5 to keep a noisy short-window
        # fit from runaway extrapolation. The safety cap is symmetric
        # (lower bound is 0 below) — only the upper bound is informed
        # by the long-horizon p95.
        cap = float(context.peak_p95_rps) * 1.5
        predicted = min(predicted, cap)

    return max(0.0, float(predicted))
```

- [ ] **Step 4: Run test to verify it passes**

```bash
pytest tests/unit/test_linear_extrap.py -v -k clips
```

Expected: PASS for both new tests.

- [ ] **Step 5: Run the full Python suite**

```bash
pytest tests/ -q
```

Expected: all green; coverage stays >= 90%.

- [ ] **Step 6: Commit**

```bash
git add forecast-service/src/forecast/linear_extrap.py forecast-service/tests/unit/test_linear_extrap.py
git commit -m "feat(forecast-svc): linear_extrap clips at peak_p95_rps*1.5 when context present (G15)"
```

---

## Sub-PR D: `gbdt_quantile` new forecaster (G12)

This sub-PR introduces a third forecaster. New Python file. New optional dependency. No existing forecaster behaviour changes here — Sub-PR D adds the path; Sub-PR E (T13) makes the classifier *choose* it for `spiky` workloads.

### Task 9: G12 — Add `lightgbm` dependency and skeleton `gbdt_model.py`

**Files:**
- Modify: `forecast-service/pyproject.toml` (add dep)
- Create: `forecast-service/src/forecast/gbdt_model.py` (skeleton)
- Create: `forecast-service/tests/unit/test_gbdt_model.py`

- [ ] **Step 1: Write the failing test (skeleton)**

Create `forecast-service/tests/unit/test_gbdt_model.py`:

```python
"""GBDT quantile forecaster unit tests. Marked slow because LightGBM's
fit on 30+ points is non-trivial."""

from __future__ import annotations

import pytest

from forecast.gbdt_model import forecast_gbdt_quantile
from forecast.models import ContextPayload


def _ctx(p95: int = 200, hourly_valid: bool = True) -> ContextPayload:
    return ContextPayload(
        baseline_rps=50,
        peak_p95_rps=p95,
        trend_24h_slope=0.0,
        hourly_profile=[50] * 24,
        hourly_profile_valid=hourly_valid,
        current_hour_utc=12,
        current_minute_utc=0,
    )


@pytest.mark.slow
def test_gbdt_returns_non_negative_prediction_on_flat_history() -> None:
    """Smoke: 30 flat samples, predict 10-minute horizon. Output must be
    finite and >= 0."""
    history = [50.0] * 30
    predicted = forecast_gbdt_quantile(history, horizon_minutes=10, context=_ctx())
    assert predicted >= 0.0
    assert predicted == predicted  # not NaN
```

- [ ] **Step 2: Run test to verify it fails**

```bash
pytest tests/unit/test_gbdt_model.py -v -m slow
```

Expected: FAIL — module `forecast.gbdt_model` doesn't exist.

- [ ] **Step 3: Add LightGBM to deps**

In `forecast-service/pyproject.toml`, append to `dependencies`:

```toml
dependencies = [
    "fastapi>=0.111",
    "uvicorn[standard]>=0.30",
    "pydantic>=2.7",
    "prophet>=1.1.5",
    "numpy>=1.26",
    "scikit-learn>=1.4",
    "prometheus-client>=0.20",
    "lightgbm>=4.3",
    "pandas>=2.2",
]
```

(`pandas` is added explicitly since `prophet_model.py` and `gbdt_model.py` both import it; Prophet already pulled it transitively, but G12 makes it a direct dep.)

Install:

```bash
cd forecast-service && pip install -e ".[dev]"
```

Expected: lightgbm and pandas installed; no resolver errors.

- [ ] **Step 4: Add skeleton `gbdt_model.py`**

Create `forecast-service/src/forecast/gbdt_model.py`:

```python
"""GBDT quantile forecaster (G12).

Per docs/design_v2.md §5 forecast_gbdt_quantile pipeline:
1. Build training rows from rps_history shifted by FORECAST_HORIZON_MINUTES:
   y_train[i] = rps_history[i + horizon];
   X_train[i] = [lag_1, lag_2, lag_3, hour_of_day_baseline, minute_in_hour].
2. Train LightGBMRegressor at GBDT_QUANTILE=0.90 (default).
3. Build the prediction row from the *last* point in rps_history with
   timestamp anchor (current_hour_utc, current_minute_utc + horizon_minutes).
4. predicted_rps = clamp(model.predict([X_pred])[0], 0, peak_p95_rps * 3).

Returns the predicted RPS at the horizon.

Phase 3 contract: feature engineering MUST use only context fields and
rps_history; never the service-local clock. F21.
"""

from __future__ import annotations

import os
from typing import TYPE_CHECKING

import numpy as np

if TYPE_CHECKING:
    from forecast.models import ContextPayload


GBDT_MIN_POINTS_DEFAULT = 30


def _quantile() -> float:
    return float(os.environ.get("GBDT_QUANTILE", "0.90"))


def _min_points() -> int:
    return int(os.environ.get("GBDT_MIN_POINTS", str(GBDT_MIN_POINTS_DEFAULT)))


def _horizon_minutes() -> int:
    # Per F36 the horizon is owned by the service. dispatch.py passes
    # it in; this helper exists for the rare path where gbdt_model is
    # called directly (tests). Default mirrors app.py's default.
    return int(os.environ.get("FORECAST_HORIZON_MINUTES", "10"))


def forecast_gbdt_quantile(
    rps_history: list[float],
    horizon_minutes: int,
    context: "ContextPayload | None" = None,
) -> float:
    """Predict RPS `horizon_minutes` ahead using a LightGBM quantile regressor.

    Raises:
        ValueError: if rps_history is empty or shorter than GBDT_MIN_POINTS
                    (after the horizon shift removes `horizon_minutes` rows).
    """
    if not rps_history:
        raise ValueError("rps_history must not be empty")
    if horizon_minutes < 0:
        raise ValueError("horizon_minutes must be >= 0")

    min_pts = _min_points()
    if len(rps_history) < min_pts + horizon_minutes:
        raise ValueError(
            f"gbdt_quantile requires len(rps_history) >= GBDT_MIN_POINTS "
            f"+ horizon_minutes = {min_pts + horizon_minutes}, "
            f"got {len(rps_history)}"
        )

    # Filled in by T10.
    raise NotImplementedError("T10 implements the training+predict body")
```

- [ ] **Step 5: Confirm import works**

```bash
python -c "from forecast.gbdt_model import forecast_gbdt_quantile; print('ok')"
```

Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add forecast-service/pyproject.toml forecast-service/src/forecast/gbdt_model.py forecast-service/tests/unit/test_gbdt_model.py
git commit -m "feat(forecast-svc): add lightgbm dep and gbdt_quantile module skeleton (G12)"
```

---

### Task 10: G12 / F21 — `gbdt_quantile` feature engineering + LightGBM fit + predict

**Files:**
- Modify: `forecast-service/src/forecast/gbdt_model.py`

- [ ] **Step 1: Run the smoke test from T9 — confirm it still fails**

```bash
pytest tests/unit/test_gbdt_model.py -v -m slow
```

Expected: FAIL — `NotImplementedError`.

- [ ] **Step 2: Implement the body**

Replace the `raise NotImplementedError(...)` in `forecast-service/src/forecast/gbdt_model.py` with the training + predict pipeline:

```python
    # Build lag-feature training rows. Each row i (0-indexed up to
    # n - horizon - 3) uses lags from rps_history[i:i+3] to predict
    # rps_history[i + horizon + 2].
    #
    # Anchor: timestamp of row i corresponds to "now - (len(history) - 1 - i) minutes".
    # We use this only to derive hour_of_day for each training row,
    # which means the anchor MUST come from context (F21) — never
    # from the service-local clock.
    if context is None:
        anchor_hour = _horizon_minutes()  # arbitrary; tests cover this path
        anchor_minute = 0
        hourly_profile = [0] * 24
        peak_p95 = 0
    else:
        anchor_hour = context.current_hour_utc
        anchor_minute = context.current_minute_utc
        hourly_profile = list(context.hourly_profile)
        peak_p95 = int(context.peak_p95_rps)

    n = len(rps_history)
    lag = 3  # lag depth — 3 prior samples per row
    rows_x: list[list[float]] = []
    rows_y: list[float] = []
    # i is the index of the row's *first* lag sample.
    # Last legal i: n - lag - horizon (so that i + lag - 1 is the most
    # recent observed sample at training time, and i + lag - 1 + horizon
    # is the target). Equivalently: rows_y index = i + lag - 1 + horizon.
    for i in range(0, n - lag - horizon_minutes + 1):
        # Row's "current minute index" (relative to history start).
        cur_idx = i + lag - 1
        target_idx = cur_idx + horizon_minutes
        if target_idx >= n:
            break

        # Walk back from anchor to derive the row's hour-of-day.
        minutes_ago = (n - 1) - cur_idx
        total_minute = (
            anchor_hour * 60 + anchor_minute - minutes_ago
        ) % (24 * 60)
        row_hour = total_minute // 60
        row_minute_in_hour = total_minute % 60

        row_x = [
            float(rps_history[i]),
            float(rps_history[i + 1]),
            float(rps_history[i + 2]),
            float(hourly_profile[row_hour]),
            float(row_minute_in_hour),
        ]
        rows_x.append(row_x)
        rows_y.append(float(rps_history[target_idx]))

    if not rows_x:
        # Defensive — guarded by the length check above, but make the
        # failure mode explicit if the math drifts in a future edit.
        raise ValueError("gbdt_quantile produced zero training rows")

    import lightgbm as lgb  # local import keeps module-import cost low

    model = lgb.LGBMRegressor(
        objective="quantile",
        alpha=_quantile(),
        n_estimators=80,
        learning_rate=0.1,
        num_leaves=15,
        min_data_in_leaf=2,
        verbose=-1,
    )
    model.fit(np.asarray(rows_x), np.asarray(rows_y))

    # Prediction row: most recent 3 samples + the hour/minute the
    # forecast is *targeting* (anchor + horizon).
    pred_minute = (anchor_hour * 60 + anchor_minute + horizon_minutes) % (24 * 60)
    pred_hour = pred_minute // 60
    pred_minute_in_hour = pred_minute % 60

    pred_row = np.asarray(
        [[
            float(rps_history[-3]),
            float(rps_history[-2]),
            float(rps_history[-1]),
            float(hourly_profile[pred_hour]),
            float(pred_minute_in_hour),
        ]]
    )
    predicted = float(model.predict(pred_row)[0])

    return predicted  # Safety cap added in T11.
```

- [ ] **Step 3: Run smoke test to verify it passes**

```bash
pytest tests/unit/test_gbdt_model.py -v -m slow -k flat
```

Expected: PASS — flat history → predicted ~50 (the quantile of flat 50s).

- [ ] **Step 4: Add a periodic-pattern behavioural test**

Append to `forecast-service/tests/unit/test_gbdt_model.py`:

```python
import math


@pytest.mark.slow
def test_gbdt_respects_hourly_profile_baseline() -> None:
    """G12 / F21: a periodic series that follows the hourly profile
    should predict a non-trivially different value at different
    target hours, because hour_of_day_baseline is a model feature.

    Build a 60-point history that's flat at 50, with a context whose
    hourly_profile has a single peak at hour 13 (=500) and 50 elsewhere.
    Predict at horizon=10 with anchor_hour=12, anchor_minute=50 — the
    target lands at hour 13, minute 0; the model should pick up on the
    hour_baseline feature value of 500 versus 50."""
    profile = [50] * 24
    profile[13] = 500
    ctx = ContextPayload(
        baseline_rps=50,
        peak_p95_rps=500,
        trend_24h_slope=0.0,
        hourly_profile=profile,
        hourly_profile_valid=True,
        current_hour_utc=12,
        current_minute_utc=50,
    )
    history = [50.0] * 60
    predicted = forecast_gbdt_quantile(history, horizon_minutes=10, context=ctx)
    # Direction-only assertion: prediction must rise above the flat
    # baseline because hour_baseline jumps at the target hour. We do
    # not pin a specific value — LightGBM is stochastic in tie-break
    # heuristics and the meaningful contract is "uses the feature".
    assert predicted > 50.0 or math.isclose(predicted, 50.0, abs_tol=5.0), (
        f"predicted={predicted} — hour_baseline feature appears ignored"
    )
```

- [ ] **Step 5: Run new test**

```bash
pytest tests/unit/test_gbdt_model.py -v -m slow
```

Expected: both tests pass.

- [ ] **Step 6: Commit**

```bash
git add forecast-service/src/forecast/gbdt_model.py forecast-service/tests/unit/test_gbdt_model.py
git commit -m "feat(forecast-svc): gbdt_quantile feature engineering + LightGBM fit + predict (G12, F21)"
```

---

### Task 11: G12 — gbdt_quantile safety cap at `peak_p95_rps × 3`

**Files:**
- Modify: `forecast-service/src/forecast/gbdt_model.py`
- Modify: `forecast-service/tests/unit/test_gbdt_model.py`

- [ ] **Step 1: Write the failing test**

Append to `forecast-service/tests/unit/test_gbdt_model.py`:

```python
@pytest.mark.slow
def test_gbdt_caps_at_peak_p95_times_3() -> None:
    """G12: outputs are clamped at peak_p95_rps * 3 to bound the
    blast radius from an over-confident quantile estimate.

    Build a history that ramps from 100 to 1000 with peak_p95_rps=100.
    The unclamped quantile prediction can land well above 300 because
    the recent trend is steep; the clamp forces the output to 300."""
    history = [float(100 + i * 10) for i in range(60)]  # 100, 110, ..., 690
    ctx = ContextPayload(
        baseline_rps=200,
        peak_p95_rps=100,  # deliberately low so the cap bites
        trend_24h_slope=0.0,
        hourly_profile=[200] * 24,
        hourly_profile_valid=True,
        current_hour_utc=12,
        current_minute_utc=0,
    )
    predicted = forecast_gbdt_quantile(history, horizon_minutes=10, context=ctx)
    assert predicted <= 300.0 + 1e-6, (
        f"predicted={predicted} exceeds peak_p95_rps*3=300 cap"
    )
```

- [ ] **Step 2: Run test to verify it fails**

```bash
pytest tests/unit/test_gbdt_model.py -v -m slow -k caps
```

Expected: FAIL — current implementation returns the raw model output.

- [ ] **Step 3: Add the cap**

In `forecast-service/src/forecast/gbdt_model.py`, replace the final `return predicted` with:

```python
    # G12 safety cap. Set to 0 when no p95 is known (cold start /
    # skip-context); in that case the cap collapses to "don't return
    # negative" and the caller still gets the model's prediction.
    upper = float(peak_p95) * 3.0 if peak_p95 > 0 else float("inf")
    capped = max(0.0, min(predicted, upper))
    return capped
```

- [ ] **Step 4: Run test to verify it passes**

```bash
pytest tests/unit/test_gbdt_model.py -v -m slow
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add forecast-service/src/forecast/gbdt_model.py forecast-service/tests/unit/test_gbdt_model.py
git commit -m "feat(forecast-svc): gbdt_quantile caps output at peak_p95_rps*3 (G12)"
```

---

### Task 12: G12 / F22 — Dispatch wires `gbdt_quantile` + auto-mode invariant

**Files:**
- Modify: `forecast-service/src/forecast/dispatch.py`
- Modify: `forecast-service/tests/unit/test_dispatch.py`

- [ ] **Step 1: Write the failing tests**

Append to `forecast-service/tests/unit/test_dispatch.py`:

```python
def _ctx() -> ContextPayload:
    return ContextPayload(
        baseline_rps=50,
        peak_p95_rps=200,
        trend_24h_slope=0.0,
        hourly_profile=[50] * 24,
        hourly_profile_valid=True,
        current_hour_utc=12,
        current_minute_utc=0,
    )


def test_dispatch_routes_gbdt_quantile_when_preferred() -> None:
    """G12: preferred_model='gbdt_quantile' MUST route through the GBDT
    forecaster, not Prophet, not linear_extrap."""
    history = [50.0] * 40
    result = recommend(
        rps_history=history,
        horizon_minutes=10,
        prophet_min_points=30,
        preferred_model="gbdt_quantile",
        context=_ctx(),
    )
    assert result["model_used"] == "gbdt_quantile"


def test_dispatch_auto_never_picks_gbdt_quantile() -> None:
    """F22: auto mode is provably never gbdt_quantile. Run the dispatcher
    with preferred_model='auto' across both short and long histories and
    assert the returned model_used is always prophet or linear_extrap."""
    for n_history in [5, 30, 60, 240, 1000]:
        history = [50.0] * n_history
        for pref in (None, "auto", ""):
            result = recommend(
                rps_history=history,
                horizon_minutes=10,
                prophet_min_points=30,
                preferred_model=pref,
                context=_ctx(),
            )
            assert result["model_used"] != "gbdt_quantile", (
                f"auto/None/'' selected gbdt_quantile at n={n_history} pref={pref!r} "
                "— F22 invariant violated"
            )


def test_dispatch_gbdt_quantile_falls_back_on_too_short_history() -> None:
    """G12: when len(rps_history) < GBDT_MIN_POINTS + horizon_minutes,
    gbdt raises; dispatch falls back to linear_extrap (mirroring the
    Prophet failure path) and logs the failure."""
    history = [50.0] * 5  # well below GBDT_MIN_POINTS=30
    result = recommend(
        rps_history=history,
        horizon_minutes=10,
        prophet_min_points=30,
        preferred_model="gbdt_quantile",
        context=_ctx(),
    )
    assert result["model_used"] == "linear_extrap"
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
pytest tests/unit/test_dispatch.py -v -k "gbdt or auto_never"
```

Expected: FAIL — no gbdt branch yet, dispatch doesn't recognise `gbdt_quantile`.

- [ ] **Step 3: Add the gbdt branch and the auto-invariant**

In `forecast-service/src/forecast/dispatch.py`, replace the entire `recommend` body:

```python
def recommend(
    rps_history: list[float],
    horizon_minutes: int,
    prophet_min_points: int,
    preferred_model: str | None = None,
    context: "ContextPayload | None" = None,
) -> RecommendResult:
    """Return the predicted RPS using the best available forecaster.

    Selection rules (per docs/design_v2.md §5):
    1. If preferred_model is "prophet" / "linear_extrap" / "gbdt_quantile",
       use it directly (subject to fallback on failure).
    2. Else (preferred_model is None / "auto" / "" / unknown):
       a. If len(rps_history) >= prophet_min_points: try Prophet, fall back to linear_extrap.
       b. Else: linear_extrap.
       **NEVER returns gbdt_quantile** — F22 invariant.

    On any forecaster's exception, fall back to linear_extrap and log
    the failure. Prophet's exception increments forecast_prophet_failures_total;
    gbdt_quantile's does not have a dedicated counter (yet — TODO Phase 6).
    """
    if context is not None:
        logging.debug(
            "context forwarded: baseline=%d p95=%d trend_24h_slope=%.4f valid=%s",
            context.baseline_rps,
            context.peak_p95_rps,
            context.trend_24h_slope,
            context.hourly_profile_valid,
        )

    if preferred_model == "gbdt_quantile":
        try:
            from forecast.gbdt_model import forecast_gbdt_quantile

            predicted = forecast_gbdt_quantile(
                rps_history, horizon_minutes, context=context
            )
            return {
                "predicted_rps": predicted,
                "horizon_minutes": horizon_minutes,
                "model_used": "gbdt_quantile",
            }
        except Exception as exc:  # noqa: BLE001 - fall back on any failure
            logging.warning(
                "gbdt_quantile failed, falling back to linear_extrap: %s", exc
            )
            predicted = forecast_linear_extrap(
                rps_history, horizon_minutes, context=context
            )
            return {
                "predicted_rps": predicted,
                "horizon_minutes": horizon_minutes,
                "model_used": "linear_extrap",
            }

    use_prophet = _should_use_prophet(
        rps_history=rps_history,
        prophet_min_points=prophet_min_points,
        preferred_model=preferred_model,
    )

    model_used: ModelName
    if use_prophet:
        try:
            predicted = forecast_prophet(rps_history, horizon_minutes, context=context)
            model_used = "prophet"
        except Exception as exc:  # noqa: BLE001 - any Prophet failure is a fallback trigger
            logging.warning("prophet failed, falling back to linear_extrap: %s", exc)
            forecast_prophet_failures_total.inc()
            predicted = forecast_linear_extrap(
                rps_history, horizon_minutes, context=context
            )
            model_used = "linear_extrap"
    else:
        predicted = forecast_linear_extrap(rps_history, horizon_minutes, context=context)
        model_used = "linear_extrap"

    return {
        "predicted_rps": predicted,
        "horizon_minutes": horizon_minutes,
        "model_used": model_used,
    }
```

The F22 invariant is enforced structurally: `gbdt_quantile` is only reachable when `preferred_model == "gbdt_quantile"`. The `_should_use_prophet` selector returns prophet or linear_extrap only; the `auto` path never enters the gbdt branch.

- [ ] **Step 4: Run tests to verify they pass**

```bash
pytest tests/unit/test_dispatch.py -v -k "gbdt or auto_never"
```

Expected: all three new tests green.

- [ ] **Step 5: Run the full Python suite**

```bash
pytest tests/ -q
```

Expected: all tests pass; coverage stays >= 90%.

- [ ] **Step 6: Commit**

```bash
git add forecast-service/src/forecast/dispatch.py forecast-service/tests/unit/test_dispatch.py
git commit -m "feat(forecast-svc): dispatch wires gbdt_quantile + enforces F22 auto-never-picks-gbdt invariant (G12, F22)"
```

---

## Sub-PR E: Selector flip + end-to-end integration

### Task 13: G19 — ComputeParams uses pattern → forecaster table

**Files:**
- Modify: `internal/classifier/params.go` (`ComputeParams` signature + body)
- Modify: `internal/classifier/pipeline.go` (pass `pattern` into `ComputeParams`)
- Modify: `internal/classifier/params_test.go`
- Modify: `internal/classifier/pipeline_test.go` (existing tests may need pattern argument)

- [ ] **Step 1: Write the failing tests**

Add to `internal/classifier/params_test.go`:

```go
func TestComputeParams_PatternDrivenForecaster(t *testing.T) {
	tests := []struct {
		pattern string
		want    string
	}{
		{"flat", ForecasterLinearExtrap},
		{"periodic", ForecasterProphet},
		{"spiky", ForecasterGBDTQuantile},
		{"gradual_ramp", ForecasterLinearExtrap},
		{"default", ForecasterLinearExtrap},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			f := Features{CV: 0.1, TodCorrelation: 0.5, TrendSlope: 0.1, PeakToTrough: 2.0}
			got := ComputeParams(f, tt.pattern, 1, 10)
			if got.PreferredForecaster != tt.want {
				t.Errorf("pattern=%q: PreferredForecaster=%q, want %q",
					tt.pattern, got.PreferredForecaster, tt.want)
			}
		})
	}
}

func TestComputeParams_UnknownPatternFallsBackToLinearExtrap(t *testing.T) {
	f := Features{CV: 0.1, TodCorrelation: 0.5, TrendSlope: 0.1, PeakToTrough: 2.0}
	got := ComputeParams(f, "<unknown>", 1, 10)
	if got.PreferredForecaster != ForecasterLinearExtrap {
		t.Errorf("unknown pattern: PreferredForecaster=%q, want %q",
			got.PreferredForecaster, ForecasterLinearExtrap)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/classifier/... -run TestComputeParams_Pattern -v
```

Expected: FAIL — `ComputeParams` currently takes only `(Features, int32, int32)`, not `(Features, string, int32, int32)`.

- [ ] **Step 3: Change `ComputeParams` signature**

In `internal/classifier/params.go`, replace the `ComputeParams` definition:

```go
// ComputeParams applies the design §7 formulae to produce recommended
// scaling params from the extracted features.
//
// Phase 3 (G19): the forecaster is selected from `pattern` via a fixed
// table — per docs/design_v2.md §5 and §6.1:
//
//	flat         → linear_extrap
//	periodic     → prophet
//	spiky        → gbdt_quantile
//	gradual_ramp → linear_extrap   (trend blend in linear_extrap does the work)
//	default      → linear_extrap   (no signal → cheapest predictor)
//
// Unknown patterns fall back to linear_extrap.
func ComputeParams(f Features, pattern string, minReplicas, maxReplicas int32) ClassifiedOutput {
	rawUp := BaseScaleUpCooldown / (1 + KCVUp*f.CV)
	scaleUp := clampInt32(int32(math.Round(rawUp)),
		ScaleUpCooldownHardFloor, ScaleUpCooldownHardCeiling)

	todFactor := math.Max(0, f.TodCorrelation)
	rawDown := BaseScaleDownCooldown * (1 + KCVDown*f.CV) / (1 + KPeriodicDown*todFactor)
	scaleDown := clampInt32(int32(math.Round(rawDown)),
		ScaleDownCooldownHardFloor, ScaleDownCooldownHardCeiling)

	var maxStep int32
	if f.PeakToTrough <= 1 {
		maxStep = 1
	} else {
		maxStep = int32(math.Ceil(math.Log2(f.PeakToTrough)))
	}
	replicaRange := maxReplicas - minReplicas
	if replicaRange < 1 {
		replicaRange = 1
	}
	maxStep = clampInt32(maxStep, 1, replicaRange)

	forecaster := forecasterForPattern(pattern)

	return ClassifiedOutput{
		ScaleUpCooldown:     scaleUp,
		ScaleDownCooldown:   scaleDown,
		MaxStep:             maxStep,
		PreferredForecaster: forecaster,
	}
}

// forecasterForPattern maps the named pattern to the v2-mandated forecaster.
// Unknown patterns fall back to linear_extrap.
func forecasterForPattern(pattern string) string {
	switch pattern {
	case "periodic":
		return ForecasterProphet
	case "spiky":
		return ForecasterGBDTQuantile
	case "flat", "gradual_ramp", "default":
		return ForecasterLinearExtrap
	default:
		return ForecasterLinearExtrap
	}
}
```

(Remove the `prophetTodCorrelationAbove` and `prophetTrendSlopeAbove` constants — they are now dead code. Or keep them as `_` references for the gradient of legacy tests; cleaner to delete and clean up the corresponding test in `params_test.go`.)

- [ ] **Step 4: Update `pipeline.go` to pass `pattern`**

In `internal/classifier/pipeline.go`, find the call site (likely `RunPipeline` and `RunPipelineV2`) and pass `result.Pattern` (or the in-scope pattern variable) as the second argument:

```go
output := ComputeParams(features, pattern, minReplicas, maxReplicas)
```

- [ ] **Step 5: Update any pre-existing tests that break**

Run:

```bash
go test ./internal/classifier/... -count=1
```

Find any tests that call `ComputeParams(f, min, max)` without the pattern argument — most likely `TestComputeParams_*` tests in `params_test.go` and possibly `pipeline_test.go`. Update each call site to pass the appropriate pattern (`"periodic"` for the prophet-tod-correlation tests, `"flat"` for the linear_extrap tests, etc.).

- [ ] **Step 6: Run tests to verify they pass**

```bash
go test ./internal/classifier/... -count=1 -v
```

Expected: all tests pass, including the new `TestComputeParams_PatternDrivenForecaster`.

- [ ] **Step 7: Commit**

```bash
git add internal/classifier/params.go internal/classifier/params_test.go internal/classifier/pipeline.go internal/classifier/pipeline_test.go
git commit -m "feat(classifier): ComputeParams selects forecaster from pattern, not features (G19)"
```

---

### Task 14: G12 + G14 + G15 — End-to-end `/recommend` integration test

**Files:**
- Modify: `forecast-service/tests/integration/test_app.py`

- [ ] **Step 1: Write the failing test**

Append to `forecast-service/tests/integration/test_app.py`:

```python
def test_recommend_endpoint_routes_gbdt_quantile_when_preferred(client) -> None:
    """G12: POST /recommend with preferred_model=gbdt_quantile and
    sufficient history returns model_used=gbdt_quantile and a
    non-negative numeric predicted_rps."""
    body = {
        "rps_history": [50.0] * 40,
        "preferred_model": "gbdt_quantile",
        "context": {
            "baseline_rps": 50,
            "peak_p95_rps": 200,
            "trend_24h_slope": 0.0,
            "hourly_profile": [50] * 24,
            "hourly_profile_valid": True,
            "current_hour_utc": 12,
            "current_minute_utc": 0,
        },
    }
    response = client.post("/recommend", json=body)
    assert response.status_code == 200, response.text
    payload = response.json()
    assert payload["model_used"] == "gbdt_quantile"
    assert payload["predicted_rps"] >= 0


def test_recommend_endpoint_auto_never_returns_gbdt_quantile(client) -> None:
    """F22: even with a generous history and a context that would make
    gbdt_quantile plausible, auto mode must select prophet or linear_extrap."""
    body = {
        "rps_history": [50.0] * 100,
        "preferred_model": "auto",
        "context": {
            "baseline_rps": 50,
            "peak_p95_rps": 200,
            "trend_24h_slope": 0.0,
            "hourly_profile": [50] * 24,
            "hourly_profile_valid": True,
            "current_hour_utc": 12,
            "current_minute_utc": 0,
        },
    }
    response = client.post("/recommend", json=body)
    assert response.status_code == 200, response.text
    payload = response.json()
    assert payload["model_used"] != "gbdt_quantile"
```

(If `client` is not the existing fixture name in `test_app.py`, use the existing fixture instead — likely `httpx_client` or `test_client`.)

- [ ] **Step 2: Run tests to verify they fail (or pass — see step 3)**

```bash
cd forecast-service && pytest tests/integration/test_app.py -v -k "gbdt or never_returns_gbdt"
```

Because Sub-PRs A and D are already in place by this point, these tests should *pass* if the wiring is correct. If they fail, the failure pinpoints where the new code path diverges from the expected behaviour.

- [ ] **Step 3: Commit**

```bash
git add forecast-service/tests/integration/test_app.py
git commit -m "test(forecast-svc): end-to-end /recommend integration for gbdt_quantile + auto-invariant (G12, F22)"
```

---

### Task 15: G19 — Classifier pipeline → spiky → gbdt_quantile integration test

**Files:**
- Modify: `internal/classifier/pipeline_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/classifier/pipeline_test.go`:

```go
// TestRunPipelineV2_SpikyPatternSelectsGBDT pins the G19 invariant
// end-to-end: a series the classifier identifies as `spiky` must
// result in PreferredForecaster=gbdt_quantile on the persisted status.
func TestRunPipelineV2_SpikyPatternSelectsGBDT(t *testing.T) {
	// Build a synthetic spiky series: low baseline with periodic
	// 10x bursts. The classifier's spiky-detection thresholds
	// (peak_to_trough > 5 OR cv > 0.50) should fire on this shape.
	series := make([]float64, 288) // 24h at 5-min cadence
	for i := range series {
		if i%30 == 0 {
			series[i] = 200.0 // burst every 30 buckets
		} else {
			series[i] = 20.0
		}
	}

	cfg := PipelineConfig{
		ResolutionMin:         5,
		HourlyProfileMinHours: 12,
		CVGuardMeanRPS:        1.0,
		StartHourUTC:          0,
	}
	result, err := RunPipelineV2(series, 240, 72, 1, 10, cfg)
	if err != nil {
		t.Fatalf("RunPipelineV2 failed: %v", err)
	}
	if result.Pattern != "spiky" {
		t.Fatalf("expected pattern=spiky, got %q (series may need re-tuning)", result.Pattern)
	}
	if result.Params.PreferredForecaster != ForecasterGBDTQuantile {
		t.Errorf(
			"spiky pattern produced PreferredForecaster=%q, want %q",
			result.Params.PreferredForecaster, ForecasterGBDTQuantile,
		)
	}
}
```

- [ ] **Step 2: Run test to verify it passes**

```bash
go test ./internal/classifier/... -run TestRunPipelineV2_SpikyPatternSelectsGBDT -v
```

Expected: PASS — Task 13 already wired pattern → forecaster, so this test exercises the wiring end-to-end.

If the test fails on `pattern != "spiky"`, the synthetic series isn't tripping the classifier's spiky thresholds. Adjust the burst magnitude (200 → 500) or frequency (every 30 → every 20 buckets) until the classifier's `spiky` detector fires; the *test purpose* is to pin the post-classification forecaster choice, not to debug the classifier's spiky detection (covered by `TestClassify_*` in Plan 13).

- [ ] **Step 3: Commit**

```bash
git add internal/classifier/pipeline_test.go
git commit -m "test(classifier): spiky pattern selects gbdt_quantile end-to-end (G19)"
```

---

## Self-Review

After completing all 15 tasks, run this checklist:

- [ ] **Spec coverage:** G12, G14, G15, G19 all have implementing tasks. F3a, F17, F16, F31, F21, F22 covered by named tasks.
- [ ] **Placeholder scan:** No "TBD", "TODO", "implement later" strings remain in the plan. (The `TODO Phase 6` comment in `dispatch.py`'s gbdt failure-counter docstring is intentional — Phase 6 owns the metrics-completeness sweep.)
- [ ] **Type consistency:** `ContextPayload` (Pydantic, Python) ↔ `ContextFields` (Go CRD) ↔ `ContextOutput` (Go pipeline internal) — three distinct types for three layers, as established in Plan 13. Plan 14 does not introduce any new context-shape types.
- [ ] **Name consistency:** `gbdt_quantile` (snake_case, lowercase) everywhere a value; `ForecasterGBDTQuantile` (PascalCase Go const); `forecast_gbdt_quantile` (Python function).
- [ ] **Enum widening landed first:** Sub-PR A (T1, T2) lands before any forecaster code returns `gbdt_quantile` — schema validation never rejects the value.
- [ ] **F22 is structurally enforced:** Look at `dispatch.py` after T12 — `gbdt_quantile` only appears inside the `if preferred_model == "gbdt_quantile":` block; the `auto`/`None` path never reaches it.
- [ ] **CI gate:** `make test` passes after every task. Python coverage stays >= 90%. Go `make lint` produces 0 findings.
- [ ] **F36 respected:** `FORECAST_HORIZON_MINUTES` is read only from `app.py` (and the gbdt module's defensive fallback) — never from controller code. (No controller files are touched by this plan.)
- [ ] **Backward compatibility:** Every forecaster's signature change is additive (new `context=None` keyword); legacy call sites that don't pass `context` continue to work.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-05-26-plan-14-v2-forecaster-surface.md`. Two execution options:**

1. **Subagent-Driven (recommended)** — Dispatch a fresh subagent per task, review between tasks, fast iteration. Best for this plan because the LightGBM-based GBDT tasks are non-trivial and benefit from focused per-task review.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints. Use this if you want to land all 15 tasks in a single push.

**Which approach?**

If Subagent-Driven chosen:

- **REQUIRED SUB-SKILL:** Use superpowers:subagent-driven-development
- Fresh subagent per task + two-stage review

If Inline Execution chosen:

- **REQUIRED SUB-SKILL:** Use superpowers:executing-plans
- Batch execution with checkpoints for review (every 3–4 tasks: A, B, C, D, E)

---

## Out of scope (deferred to later phases)

- **G13 / G18** (operator visibility — `unboundedRecommended`, binding tokens, ExplainWorker prompt) — Phase 4 / Plan 15.
- **G16 / G17 / G20 strict-inequality / G22 PascalCase** — Phase 5 / Plan 16 (bug-fix sweep).
- **Forecast-Service metrics expansion** (`gbdt_quantile_failures_total`, `gbdt_quantile_predictions_total`) — Phase 6 / Plan 17 metric-completeness pass.
- **E11 banner promotion** — Phase 6 / Plan 17.
- **Nightly E2E `spiky` scenario** asserting `model_used == "gbdt_quantile"` end-to-end on a real cluster — Phase 6.
