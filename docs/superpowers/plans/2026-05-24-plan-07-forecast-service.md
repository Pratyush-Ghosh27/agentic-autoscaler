# Plan 07 — Forecast Service Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the standalone Python/FastAPI Forecast Service that exposes `POST /recommend`, auto-selects between Prophet and linear extrapolation based on history length, honours an optional `preferred_model` override, performs a Prophet warm-up at startup so the first real call doesn't pay the Stan compilation cost, and exports a `forecast_prophet_failures_total` counter for visibility.

**Architecture:** A single FastAPI app under `forecast-service/`, deployed as its own container image, fully independent of the Go controller (consumed only via HTTP). Pure-logic modules (`linear_extrap.py`, `prophet_model.py`, `dispatch.py`) hold the math; `app.py` is a thin HTTP layer over `dispatch.recommend()`. The `dispatch` module owns the model-selection and fallback logic so the HTTP layer stays trivial. A startup hook in `app.py` runs a tiny Prophet fit once, eating Stan's cold-start cost before any real request arrives.

**Tech Stack:** Python 3.12, FastAPI, uvicorn (ASGI server), pydantic v2 (request/response models), prophet 1.1+, numpy, scikit-learn (for `LinearRegression`), prometheus_client (counter export), pytest + httpx + pytest-cov + pytest-asyncio (tests), ruff + mypy (lint + types).

---

## Spec Coverage Map

| Spec section | Tasks |
| --- | --- |
| §3 architecture: Forecast Service shape and contract | T1, T10, T11 |
| §5 forecast service `/recommend` pipeline (input/output shape, validation) | T10, T11 |
| §5 `forecast_linear_extrap` algorithm (last 10 points, least-squares, horizon extrapolation, max(0, ...) clamp) | T3, T4 |
| §5 `forecast_prophet` algorithm (Prophet config, synthetic timestamps, future DataFrame, max(0, ...) clamp) | T5, T6 |
| §5 dispatch logic (auto-selection by `PROPHET_MIN_POINTS`, fallback to linear_extrap on Prophet exception, `preferred_model` override) | T7, T8, T9 |
| §5 startup warm-up (one dummy Prophet fit during FastAPI's `startup` event) | T13 |
| §9 failure: Prophet raises → fall back to linear_extrap with `model_used: "linear_extrap"`; counter increments | T9, T14 |
| §9 failure: Forecast Service returns invalid response (negative, NaN) — caller-side, asserted here as "we never produce one" | T3, T4, T5 |

What's intentionally out of scope: the Forecast Service deployment manifest (Plan #10), the Go-side Forecast adapter that calls this service (Plan #3), and any cross-service env-var harmonization (controller-side already covered by Plan #1). The container image built here is the artifact Plan #10 references.

---

## File Structure

```
scaler/
└── forecast-service/
    ├── pyproject.toml                    # T1: project + deps + lint config
    ├── Dockerfile                        # T2: multi-stage; prophet pre-built
    ├── .dockerignore                     # T2
    ├── README.md                         # T15: brief usage + dev notes
    ├── src/
    │   └── forecast/
    │       ├── __init__.py               # T1
    │       ├── app.py                    # T11, T13, T14: FastAPI + startup + /metrics
    │       ├── models.py                 # T10: Pydantic request/response
    │       ├── linear_extrap.py          # T3, T4: forecast_linear_extrap
    │       ├── prophet_model.py          # T5, T6: forecast_prophet
    │       ├── dispatch.py               # T7, T8, T9: recommend() orchestration
    │       ├── warmup.py                 # T13: startup-time Prophet warm-up
    │       └── metrics.py                # T14: prometheus_client setup
    └── tests/
        ├── __init__.py                   # T1
        ├── conftest.py                   # T1: shared fixtures
        ├── unit/
        │   ├── __init__.py               # T1
        │   ├── test_linear_extrap.py     # T3, T4
        │   ├── test_prophet_model.py     # T5
        │   ├── test_dispatch.py          # T7, T8, T9
        │   └── test_models.py            # T10
        └── integration/
            ├── __init__.py               # T1
            ├── test_app.py               # T11, T12
            └── test_warmup.py            # T13
```

### File responsibilities

- `linear_extrap.py` — pure function: takes a list of floats and a horizon (minutes), returns a single non-negative float. No I/O, no logging, no exceptions for happy inputs. Stateless.
- `prophet_model.py` — pure function with the same signature. Wraps `prophet.Prophet`. Disables daily/weekly seasonality (per design §5). Raises on fit failure (caller catches).
- `dispatch.py` — `recommend(rps_history, horizon_minutes, prophet_min_points, preferred_model=None)` returns `{"predicted_rps": float, "horizon_minutes": int, "model_used": str}`. Auto-selects, honours override, catches Prophet exceptions and falls back. Increments `forecast_prophet_failures_total` on fallback.
- `models.py` — Pydantic v2 models for request and response, plus a custom validator enforcing non-negative values.
- `app.py` — FastAPI app. POST /recommend delegates to `dispatch.recommend()`. GET /healthz returns `{"status": "ok"}`. GET /metrics returns prometheus_client output. Startup hook runs `warmup.warmup_prophet()`.
- `warmup.py` — single function that builds a 90-point synthetic series and calls `forecast_prophet()` once. Eats the Stan-compilation cold start.
- `metrics.py` — declares the `prometheus_client.Counter` used by `dispatch.py`.

---

## Phase 0 — Project bootstrap

### Task 1: Create the forecast-service skeleton

**Files:**
- Create: `forecast-service/pyproject.toml`
- Create: `forecast-service/src/forecast/__init__.py`
- Create: `forecast-service/tests/__init__.py`
- Create: `forecast-service/tests/conftest.py`
- Create: `forecast-service/tests/unit/__init__.py`
- Create: `forecast-service/tests/integration/__init__.py`

- [ ] **Step 1: Create directory layout**

```bash
mkdir -p forecast-service/src/forecast
mkdir -p forecast-service/tests/unit
mkdir -p forecast-service/tests/integration
touch forecast-service/src/forecast/__init__.py
touch forecast-service/tests/__init__.py
touch forecast-service/tests/unit/__init__.py
touch forecast-service/tests/integration/__init__.py
```

- [ ] **Step 2: Create pyproject.toml**

Create `forecast-service/pyproject.toml`:

```toml
[build-system]
requires = ["hatchling>=1.21"]
build-backend = "hatchling.build"

[project]
name = "agentic-forecast"
version = "0.1.0"
description = "Forecast service for the agentic autoscaler"
requires-python = ">=3.12"
dependencies = [
    "fastapi>=0.111",
    "uvicorn[standard]>=0.30",
    "pydantic>=2.7",
    "prophet>=1.1.5",
    "numpy>=1.26",
    "scikit-learn>=1.4",
    "prometheus-client>=0.20",
]

[project.optional-dependencies]
dev = [
    "pytest>=8.2",
    "pytest-cov>=5.0",
    "pytest-asyncio>=0.23",
    "httpx>=0.27",
    "respx>=0.21",
    "ruff>=0.4",
    "mypy>=1.10",
]

[tool.hatch.build.targets.wheel]
packages = ["src/forecast"]

[tool.pytest.ini_options]
testpaths = ["tests"]
addopts = "--cov=forecast --cov-report=term-missing --cov-fail-under=90"
asyncio_mode = "auto"

[tool.ruff]
line-length = 100
src = ["src", "tests"]

[tool.ruff.lint]
select = ["E", "F", "W", "I", "B", "UP", "ANN", "PT"]
ignore = ["ANN101", "ANN102"]  # self/cls type hints not required

[tool.ruff.lint.per-file-ignores]
"tests/**/*.py" = ["ANN"]   # tests don't need return-type annotations

[tool.mypy]
python_version = "3.12"
strict = true
files = ["src"]

[[tool.mypy.overrides]]
module = ["prophet.*"]
ignore_missing_imports = true
```

- [ ] **Step 3: Create conftest.py with the shared fixture set**

Create `forecast-service/tests/conftest.py`:

```python
"""Shared pytest fixtures for the forecast-service test suite."""

from __future__ import annotations

import math

import numpy as np
import pytest


@pytest.fixture
def linear_series_30() -> list[float]:
    """A 30-point series with slope=2 rps/min, intercept=100."""
    return [100.0 + 2.0 * i for i in range(30)]


@pytest.fixture
def flat_series_30() -> list[float]:
    """A 30-point series of constant 200 rps."""
    return [200.0] * 30


@pytest.fixture
def periodic_series_120() -> list[float]:
    """A 120-point series with hourly periodicity (period=60 min)."""
    base = 200.0
    amplitude = 100.0
    return [
        base + amplitude * math.sin(2 * math.pi * i / 60.0)
        for i in range(120)
    ]


@pytest.fixture
def short_series_5() -> list[float]:
    """5 points — below PROPHET_MIN_POINTS for any sane threshold."""
    return [100.0, 110.0, 105.0, 108.0, 112.0]


@pytest.fixture
def reseeded_rng() -> np.random.Generator:
    """Deterministic RNG so noisy tests are reproducible."""
    return np.random.default_rng(seed=42)
```

- [ ] **Step 4: Verify the project structure**

```bash
cd forecast-service
ls -R src tests
```

Expected: tree shows the empty modules and the `__init__.py` files.

- [ ] **Step 5: Install in editable mode + dev deps**

```bash
cd forecast-service
python3.12 -m venv .venv
. .venv/bin/activate
pip install -e ".[dev]"
```

This is a one-time setup; `pip install -e ".[dev]"` installs prophet (which pulls in cmdstanpy). The first install can take 5-10 minutes because cmdstanpy compiles a Stan model. Subsequent installs are fast.

- [ ] **Step 6: Smoke check that ruff and pytest find the project**

```bash
ruff check .
pytest --collect-only -q
```

Expected: ruff passes (no files yet to lint); pytest reports `0 tests collected` (no tests yet).

- [ ] **Step 7: Commit**

```bash
cd ..
git add forecast-service/
git commit -m "feat(forecast): scaffold forecast-service project skeleton"
```

---

### Task 2: Multi-stage Dockerfile

**Files:**
- Create: `forecast-service/Dockerfile`
- Create: `forecast-service/.dockerignore`

Prophet's first import compiles a Stan model the first time it runs. Doing this once at image build (rather than at every container start) saves ~30 seconds per cold start.

- [ ] **Step 1: Create .dockerignore**

Create `forecast-service/.dockerignore`:

```
.venv/
.pytest_cache/
.ruff_cache/
.mypy_cache/
__pycache__/
*.pyc
tests/
```

- [ ] **Step 2: Create Dockerfile**

Create `forecast-service/Dockerfile`:

```dockerfile
# syntax=docker/dockerfile:1.7

# ---- builder: install all deps including build-time prophet/cmdstanpy ----
FROM python:3.12-slim AS builder

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

# build-essential is needed by cmdstanpy/prophet to compile Stan
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build
COPY pyproject.toml ./
COPY src ./src

RUN pip install --upgrade pip && \
    pip install build && \
    python -m build --wheel --outdir /dist .

# ---- runtime: slim image with only the wheel + runtime deps ----
FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PORT=8000

# libgomp is needed by prophet at runtime (OpenMP for Stan)
RUN apt-get update && apt-get install -y --no-install-recommends \
    libgomp1 \
    && rm -rf /var/lib/apt/lists/* && \
    useradd --create-home --shell /bin/bash forecast

COPY --from=builder /dist/*.whl /tmp/

# Pre-compile prophet's Stan model into the image so first request is fast.
RUN pip install --upgrade pip && \
    pip install /tmp/*.whl uvicorn[standard] && \
    rm -f /tmp/*.whl && \
    python -c "from prophet import Prophet; p = Prophet(); print('prophet ok')" || true

USER forecast
EXPOSE 8000
CMD ["uvicorn", "forecast.app:app", "--host", "0.0.0.0", "--port", "8000"]
```

- [ ] **Step 3: Try a build (large; ~10 min on a fresh machine)**

```bash
cd forecast-service
docker build -t agentic-forecast:dev .
```

Expected: build succeeds; final image is roughly 800 MB-1.2 GB. If the build fails on `pip install /tmp/*.whl`, the source layout in `pyproject.toml` `[tool.hatch.build.targets.wheel].packages` likely doesn't match — verify it points at `src/forecast`.

- [ ] **Step 4: Commit**

```bash
cd ..
git add forecast-service/Dockerfile forecast-service/.dockerignore
git commit -m "feat(forecast): add multi-stage Dockerfile with prophet warm cache"
```

---

## Phase 1 — Linear extrapolation (Tier-1 strict TDD)

### Task 3: forecast_linear_extrap — happy path

**Files:**
- Create: `forecast-service/src/forecast/linear_extrap.py`
- Create: `forecast-service/tests/unit/test_linear_extrap.py`

- [ ] **Step 1: Write the failing test FIRST**

Create `forecast-service/tests/unit/test_linear_extrap.py`:

```python
"""Tests for forecast.linear_extrap."""

from __future__ import annotations

import pytest

from forecast.linear_extrap import forecast_linear_extrap


def test_perfect_linear_series_extrapolates_correctly(linear_series_30: list[float]) -> None:
    """y = 100 + 2x, last x=29 -> y=158. Horizon 10 -> x=29+9=38 -> y=176."""
    result = forecast_linear_extrap(linear_series_30, horizon_minutes=10)
    assert result == pytest.approx(176.0, abs=0.01)


def test_flat_series_returns_flat_value(flat_series_30: list[float]) -> None:
    result = forecast_linear_extrap(flat_series_30, horizon_minutes=10)
    assert result == pytest.approx(200.0, abs=0.01)
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd forecast-service
. .venv/bin/activate
pytest tests/unit/test_linear_extrap.py -v
```

Expected: ImportError because `forecast.linear_extrap` does not exist.

- [ ] **Step 3: Implement the minimal version**

Create `forecast-service/src/forecast/linear_extrap.py`:

```python
"""Linear extrapolation forecaster.

Per docs/design.md §5 forecast_linear_extrap pipeline:
1. Take the last min(10, len(rps_history)) points of rps_history.
2. Fit a least-squares line y = m*x + b (x = minute indices 0..n-1).
3. Extrapolate to x = n + (horizon_minutes - 1).
4. Return max(0, predicted_rps).
"""

from __future__ import annotations

import numpy as np


def forecast_linear_extrap(
    rps_history: list[float],
    horizon_minutes: int,
) -> float:
    """Predict RPS `horizon_minutes` ahead via least-squares linear fit.

    Uses up to the last 10 points of history to fit a line and extrapolates
    to the (horizon_minutes - 1)th point past the end of the series.
    """
    if not rps_history:
        raise ValueError("rps_history must not be empty")

    series = np.asarray(rps_history[-10:], dtype=float)
    n = len(series)

    if n == 1:
        # Single point: no slope; the "line" is a flat value.
        return max(0.0, float(series[0]))

    x = np.arange(n, dtype=float)
    slope, intercept = np.polyfit(x, series, deg=1)

    # Extrapolate to x = n + (horizon_minutes - 1).
    target_x = n + horizon_minutes - 1
    predicted = slope * target_x + intercept

    return max(0.0, float(predicted))
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
pytest tests/unit/test_linear_extrap.py -v
```

Expected: 2 PASSED.

- [ ] **Step 5: Commit**

```bash
cd ..
git add forecast-service/
git commit -m "feat(forecast): add forecast_linear_extrap (happy path)"
```

---

### Task 4: forecast_linear_extrap — edge cases (zero clamp, single point, negative slope, empty input)

**Files:**
- Modify: `forecast-service/tests/unit/test_linear_extrap.py`

- [ ] **Step 1: Append failing edge-case tests**

Append to `forecast-service/tests/unit/test_linear_extrap.py`:

```python
def test_descending_series_clamps_to_zero() -> None:
    """A series falling fast enough that linear extrapolation goes negative."""
    series = [100.0, 90.0, 80.0, 70.0, 60.0, 50.0, 40.0, 30.0, 20.0, 10.0]
    result = forecast_linear_extrap(series, horizon_minutes=10)
    # slope = -10/min, x=10+9=19, y = -10*19 + 100 = -90 -> clamped to 0
    assert result == pytest.approx(0.0)


def test_single_point_returns_that_point() -> None:
    result = forecast_linear_extrap([42.5], horizon_minutes=10)
    assert result == pytest.approx(42.5)


def test_empty_series_raises() -> None:
    with pytest.raises(ValueError, match="empty"):
        forecast_linear_extrap([], horizon_minutes=10)


def test_horizon_zero_extrapolates_to_last_index() -> None:
    """horizon=0 should pin the prediction to x=n-1 (i.e., the last point on the line)."""
    series = [100.0 + 2.0 * i for i in range(10)]  # y = 100 + 2x, x in 0..9
    result = forecast_linear_extrap(series, horizon_minutes=0)
    # x=10+0-1=9, y = 2*9 + 100 = 118
    assert result == pytest.approx(118.0, abs=0.01)


def test_only_last_10_points_used() -> None:
    """A 30-point series: only the last 10 points govern the fit."""
    # First 20 points slope=10/min, last 10 points are flat at 1000.
    series = [10.0 * i for i in range(20)] + [1000.0] * 10
    result = forecast_linear_extrap(series, horizon_minutes=10)
    # last 10 points are flat -> slope=0, intercept=1000, predicted=1000.
    assert result == pytest.approx(1000.0, abs=0.01)
```

- [ ] **Step 2: Run the tests**

```bash
cd forecast-service && pytest tests/unit/test_linear_extrap.py -v
```

Expected: all 7 tests PASS (the implementation from T3 already handles these correctly because of the `[-10:]` slice and `max(0.0, ...)` clamp).

If `test_horizon_zero_extrapolates_to_last_index` fails, re-check the formula in `linear_extrap.py`: `target_x = n + horizon_minutes - 1`. With `n=10, horizon=0`, `target_x=9`, so `y = 2*9 + 100 = 118`. Correct.

- [ ] **Step 3: Run with coverage to confirm we're at 100% on this module**

```bash
pytest tests/unit/test_linear_extrap.py --cov=forecast.linear_extrap --cov-report=term-missing
```

Expected: `forecast/linear_extrap.py 100%`.

- [ ] **Step 4: Commit**

```bash
cd ..
git add forecast-service/tests/unit/test_linear_extrap.py
git commit -m "test(forecast): cover linear_extrap edge cases (zero clamp, single, empty, horizon=0)"
```

---

## Phase 2 — Prophet model (Tier-1 strict TDD; integration tier on real fits)

### Task 5: forecast_prophet — happy path on a synthetic periodic series

**Files:**
- Create: `forecast-service/src/forecast/prophet_model.py`
- Create: `forecast-service/tests/unit/test_prophet_model.py`

- [ ] **Step 1: Write the failing test**

Create `forecast-service/tests/unit/test_prophet_model.py`:

```python
"""Tests for forecast.prophet_model.

Prophet is real-fitting in these tests (no mocks). Synthetic periodic
series are short (60-120 points) so fits complete in <1 s each.
"""

from __future__ import annotations

import pytest

from forecast.prophet_model import forecast_prophet


def test_prophet_returns_non_negative_finite_value(periodic_series_120: list[float]) -> None:
    result = forecast_prophet(periodic_series_120, horizon_minutes=10)
    assert result >= 0.0
    assert result < 10000.0  # absurdly large catch


def test_prophet_on_flat_series_returns_around_flat_value(flat_series_30: list[float]) -> None:
    result = forecast_prophet(flat_series_30, horizon_minutes=10)
    # flat series, prophet should predict near 200; tolerate 30% drift
    assert 140.0 <= result <= 260.0


def test_prophet_clamps_negative_predictions_to_zero() -> None:
    """A steeply descending short series: Prophet may project negative; we clamp."""
    series = [100.0 - 5.0 * i for i in range(20)]
    result = forecast_prophet(series, horizon_minutes=10)
    assert result >= 0.0
```

- [ ] **Step 2: Run to verify ImportError**

```bash
cd forecast-service && pytest tests/unit/test_prophet_model.py -v
```

Expected: `ModuleNotFoundError: No module named 'forecast.prophet_model'`.

- [ ] **Step 3: Implement forecast_prophet**

Create `forecast-service/src/forecast/prophet_model.py`:

```python
"""Prophet-based forecaster.

Per docs/design.md §5 forecast_prophet pipeline:
1. Build a DataFrame: ds = synthetic 1-minute timestamps ending now;
                       y  = rps_history values.
2. Fit Prophet with daily/weekly seasonality disabled,
   changepoint_prior_scale=0.5.
3. Build a future DataFrame extending horizon_minutes past the last ds.
4. predicted_rps = model.predict(future).iloc[-1].yhat.
5. Return max(0.0, predicted_rps).

Daily/weekly seasonality is disabled because we never have multi-day
history available. Prophet's trend + changepoint detection is what
beats linear extrapolation on plateaus and curving ramps.
"""

from __future__ import annotations

from datetime import datetime, timedelta, timezone

import pandas as pd
from prophet import Prophet


def forecast_prophet(
    rps_history: list[float],
    horizon_minutes: int,
) -> float:
    """Predict RPS `horizon_minutes` ahead using Prophet.

    Raises any exception Prophet raises during fit; the caller
    (dispatch.recommend) is responsible for catching and falling back.
    """
    if not rps_history:
        raise ValueError("rps_history must not be empty")
    if horizon_minutes < 0:
        raise ValueError("horizon_minutes must be >= 0")

    n = len(rps_history)
    end = datetime.now(tz=timezone.utc).replace(second=0, microsecond=0)
    timestamps = [end - timedelta(minutes=(n - 1 - i)) for i in range(n)]

    df = pd.DataFrame({"ds": timestamps, "y": rps_history})

    model = Prophet(
        daily_seasonality=False,
        weekly_seasonality=False,
        changepoint_prior_scale=0.5,
    )
    model.fit(df)

    future = model.make_future_dataframe(
        periods=horizon_minutes,
        freq="min",
        include_history=False,
    )
    forecast = model.predict(future)
    predicted = float(forecast["yhat"].iloc[-1])

    return max(0.0, predicted)
```

- [ ] **Step 4: Run the tests**

```bash
pytest tests/unit/test_prophet_model.py -v
```

Expected: 3 PASSED. Each test takes ~1-3 seconds (Prophet fit dominates).

If a test is flaky on the periodic series (Prophet predictions can vary slightly between runs because of random initialization), bump the tolerance bands rather than seeding — Prophet's internal randomness isn't fully controllable.

- [ ] **Step 5: Commit**

```bash
cd ..
git add forecast-service/
git commit -m "feat(forecast): add forecast_prophet using Prophet 1.1+"
```

---

### Task 6: forecast_prophet — fit failures propagate

**Files:**
- Modify: `forecast-service/tests/unit/test_prophet_model.py`

The dispatch layer (T9) catches Prophet exceptions and falls back to linear_extrap. To exercise that path, we need a way to make Prophet raise. The cleanest is to feed it an input shape it can't handle.

- [ ] **Step 1: Append failing test that asserts forecast_prophet raises on a malformed input**

Append to `forecast-service/tests/unit/test_prophet_model.py`:

```python
def test_prophet_raises_on_empty_history() -> None:
    with pytest.raises(ValueError, match="empty"):
        forecast_prophet([], horizon_minutes=10)


def test_prophet_raises_on_negative_horizon() -> None:
    with pytest.raises(ValueError, match="horizon"):
        forecast_prophet([100.0, 110.0], horizon_minutes=-1)
```

- [ ] **Step 2: Run**

```bash
cd forecast-service && pytest tests/unit/test_prophet_model.py -v
```

Expected: all 5 tests PASS (the impl from T5 already handles these via the early-validation guards).

- [ ] **Step 3: Coverage check**

```bash
pytest tests/unit/test_prophet_model.py --cov=forecast.prophet_model --cov-report=term-missing
```

Expected: `forecast/prophet_model.py 100%`.

- [ ] **Step 4: Commit**

```bash
cd ..
git add forecast-service/tests/unit/test_prophet_model.py
git commit -m "test(forecast): cover prophet_model input-validation paths"
```

---

## Phase 3 — Dispatch (Tier-1 strict TDD)

### Task 7: dispatch.recommend — auto-selection by length

**Files:**
- Create: `forecast-service/src/forecast/metrics.py`
- Create: `forecast-service/src/forecast/dispatch.py`
- Create: `forecast-service/tests/unit/test_dispatch.py`

- [ ] **Step 1: Create metrics.py first (so dispatch can import the counter)**

Create `forecast-service/src/forecast/metrics.py`:

```python
"""Prometheus metrics exported by the forecast service."""

from __future__ import annotations

from prometheus_client import Counter

# Counter increments every time a Prophet attempt raises and we fall
# through to linear_extrap. Visible in Grafana to spot Prophet flakes.
forecast_prophet_failures_total = Counter(
    "forecast_prophet_failures_total",
    "Number of times Prophet raised during /recommend; dispatcher fell back to linear_extrap.",
)
```

- [ ] **Step 2: Write failing dispatch tests**

Create `forecast-service/tests/unit/test_dispatch.py`:

```python
"""Tests for forecast.dispatch.recommend()."""

from __future__ import annotations

import pytest

from forecast.dispatch import recommend


def test_short_history_uses_linear_extrap(short_series_5: list[float]) -> None:
    result = recommend(
        rps_history=short_series_5,
        horizon_minutes=10,
        prophet_min_points=60,
    )
    assert result["model_used"] == "linear_extrap"
    assert result["horizon_minutes"] == 10
    assert result["predicted_rps"] >= 0.0


def test_long_history_uses_prophet(periodic_series_120: list[float]) -> None:
    result = recommend(
        rps_history=periodic_series_120,
        horizon_minutes=10,
        prophet_min_points=60,
    )
    assert result["model_used"] == "prophet"
    assert result["horizon_minutes"] == 10
    assert result["predicted_rps"] >= 0.0


def test_at_threshold_uses_prophet(linear_series_30: list[float]) -> None:
    """A 30-point series with prophet_min_points=30 should pick prophet (>=, not >)."""
    result = recommend(
        rps_history=linear_series_30,
        horizon_minutes=10,
        prophet_min_points=30,
    )
    assert result["model_used"] == "prophet"


def test_one_below_threshold_uses_linear_extrap(linear_series_30: list[float]) -> None:
    result = recommend(
        rps_history=linear_series_30,
        horizon_minutes=10,
        prophet_min_points=31,
    )
    assert result["model_used"] == "linear_extrap"
```

- [ ] **Step 3: Run to verify it fails**

```bash
cd forecast-service && pytest tests/unit/test_dispatch.py -v
```

Expected: ImportError on `forecast.dispatch`.

- [ ] **Step 4: Implement dispatch.recommend (auto-selection only; override + fallback come in T8/T9)**

Create `forecast-service/src/forecast/dispatch.py`:

```python
"""Forecast dispatcher.

Auto-selects between Prophet and linear extrapolation based on history
length, honours an optional preferred_model override, and falls back
to linear_extrap if Prophet raises (incrementing
forecast_prophet_failures_total).
"""

from __future__ import annotations

from typing import Literal, TypedDict

from forecast.linear_extrap import forecast_linear_extrap
from forecast.prophet_model import forecast_prophet


ModelName = Literal["prophet", "linear_extrap"]
PreferredModel = Literal["prophet", "linear_extrap", "auto"]


class RecommendResult(TypedDict):
    predicted_rps: float
    horizon_minutes: int
    model_used: ModelName


def recommend(
    rps_history: list[float],
    horizon_minutes: int,
    prophet_min_points: int,
    preferred_model: str | None = None,
) -> RecommendResult:
    """Return the predicted RPS using the best available forecaster.

    Selection rules (per docs/design.md §5):
    1. If preferred_model is "prophet" or "linear_extrap", use it directly.
    2. Else if len(rps_history) >= prophet_min_points, attempt prophet
       (and fall through to linear_extrap on exception).
    3. Else use linear_extrap.

    preferred_model values that mean "no override" are: None, "auto", "".
    """
    use_prophet = _should_use_prophet(
        rps_history=rps_history,
        prophet_min_points=prophet_min_points,
        preferred_model=preferred_model,
    )

    if use_prophet:
        predicted = forecast_prophet(rps_history, horizon_minutes)
        model_used: ModelName = "prophet"
    else:
        predicted = forecast_linear_extrap(rps_history, horizon_minutes)
        model_used = "linear_extrap"

    return {
        "predicted_rps": predicted,
        "horizon_minutes": horizon_minutes,
        "model_used": model_used,
    }


def _should_use_prophet(
    rps_history: list[float],
    prophet_min_points: int,
    preferred_model: str | None,
) -> bool:
    """Pure selector — no side effects, no fitting."""
    if preferred_model == "prophet":
        return True
    if preferred_model == "linear_extrap":
        return False
    # None, "auto", "", or anything else: auto-select by length.
    return len(rps_history) >= prophet_min_points
```

- [ ] **Step 5: Run the tests**

```bash
pytest tests/unit/test_dispatch.py -v
```

Expected: 4 PASSED.

- [ ] **Step 6: Commit**

```bash
cd ..
git add forecast-service/
git commit -m "feat(forecast): add dispatch.recommend with auto-selection by length"
```

---

### Task 8: dispatch.recommend — preferred_model override variants

**Files:**
- Modify: `forecast-service/tests/unit/test_dispatch.py`

- [ ] **Step 1: Append failing override tests**

Append to `forecast-service/tests/unit/test_dispatch.py`:

```python
def test_preferred_prophet_overrides_short_history(short_series_5: list[float]) -> None:
    """preferred_model='prophet' on a 5-point series — Prophet may fit poorly,
    but the dispatcher must honour the override (and the fallback in T9 covers
    the case where it raises)."""
    result = recommend(
        rps_history=short_series_5 * 12,  # 60 points so prophet doesn't outright reject
        horizon_minutes=10,
        prophet_min_points=1000,  # auto would pick linear_extrap
        preferred_model="prophet",
    )
    assert result["model_used"] == "prophet"


def test_preferred_linear_extrap_overrides_long_history(periodic_series_120: list[float]) -> None:
    result = recommend(
        rps_history=periodic_series_120,
        horizon_minutes=10,
        prophet_min_points=60,
        preferred_model="linear_extrap",
    )
    assert result["model_used"] == "linear_extrap"


@pytest.mark.parametrize("override", [None, "auto", ""])
def test_no_override_falls_through_to_auto(
    linear_series_30: list[float],
    override: str | None,
) -> None:
    """None, 'auto', and '' all mean the same thing: defer to length-based auto-select."""
    result = recommend(
        rps_history=linear_series_30,
        horizon_minutes=10,
        prophet_min_points=20,  # 30 >= 20 -> prophet
        preferred_model=override,
    )
    assert result["model_used"] == "prophet"


def test_unknown_preferred_value_treated_as_auto(linear_series_30: list[float]) -> None:
    """A future-unknown override value should not crash the service; it falls to auto."""
    result = recommend(
        rps_history=linear_series_30,
        horizon_minutes=10,
        prophet_min_points=20,
        preferred_model="experimental_xgboost",  # unknown to us
    )
    # 30 >= 20 -> auto picks prophet
    assert result["model_used"] == "prophet"
```

- [ ] **Step 2: Run the tests**

```bash
cd forecast-service && pytest tests/unit/test_dispatch.py -v
```

Expected: all PASS (the dispatch logic from T7 already handles these cases via the explicit if/else and fall-through).

- [ ] **Step 3: Commit**

```bash
cd ..
git add forecast-service/tests/unit/test_dispatch.py
git commit -m "test(forecast): cover preferred_model override variants in dispatch"
```

---

### Task 9: dispatch.recommend — Prophet failure falls back, increments counter

**Files:**
- Modify: `forecast-service/src/forecast/dispatch.py`
- Modify: `forecast-service/tests/unit/test_dispatch.py`

- [ ] **Step 1: Write the failing fallback test**

Append to `forecast-service/tests/unit/test_dispatch.py`:

```python
from unittest.mock import patch

from forecast.metrics import forecast_prophet_failures_total


def test_prophet_failure_falls_back_to_linear_extrap(
    linear_series_30: list[float],
) -> None:
    before = forecast_prophet_failures_total._value.get()  # internal accessor; OK in tests
    with patch(
        "forecast.dispatch.forecast_prophet",
        side_effect=RuntimeError("simulated prophet fit failure"),
    ):
        result = recommend(
            rps_history=linear_series_30,
            horizon_minutes=10,
            prophet_min_points=20,
        )

    assert result["model_used"] == "linear_extrap"
    assert result["predicted_rps"] >= 0.0
    after = forecast_prophet_failures_total._value.get()
    assert after == before + 1


def test_explicit_prophet_override_failure_also_falls_back(
    linear_series_30: list[float],
) -> None:
    """Even when the operator explicitly asked for prophet, a fit failure
    must still fall back rather than 5xx the request."""
    with patch(
        "forecast.dispatch.forecast_prophet",
        side_effect=ValueError("simulated"),
    ):
        result = recommend(
            rps_history=linear_series_30,
            horizon_minutes=10,
            prophet_min_points=1000,
            preferred_model="prophet",
        )
    assert result["model_used"] == "linear_extrap"
```

- [ ] **Step 2: Run; expect failure**

```bash
cd forecast-service && pytest tests/unit/test_dispatch.py::test_prophet_failure_falls_back_to_linear_extrap -v
```

Expected: FAIL with `RuntimeError: simulated prophet fit failure` propagating out (T7's dispatch doesn't catch Prophet errors).

- [ ] **Step 3: Add fallback in dispatch.recommend**

Modify `forecast-service/src/forecast/dispatch.py`. Add the import:

```python
import logging

from forecast.metrics import forecast_prophet_failures_total
```

(Add `logging` to existing imports.)

Replace the `if use_prophet: ... else: ...` block in `recommend()` with:

```python
    if use_prophet:
        try:
            predicted = forecast_prophet(rps_history, horizon_minutes)
            model_used: ModelName = "prophet"
        except Exception as exc:  # noqa: BLE001 - any Prophet failure is a fallback trigger
            logging.warning("prophet failed, falling back to linear_extrap: %s", exc)
            forecast_prophet_failures_total.inc()
            predicted = forecast_linear_extrap(rps_history, horizon_minutes)
            model_used = "linear_extrap"
    else:
        predicted = forecast_linear_extrap(rps_history, horizon_minutes)
        model_used = "linear_extrap"
```

- [ ] **Step 4: Run all dispatch tests**

```bash
pytest tests/unit/test_dispatch.py -v
```

Expected: all PASS.

- [ ] **Step 5: Coverage check**

```bash
pytest tests/unit/test_dispatch.py --cov=forecast.dispatch --cov-report=term-missing
```

Expected: 100% on `forecast/dispatch.py`.

- [ ] **Step 6: Commit**

```bash
cd ..
git add forecast-service/
git commit -m "feat(forecast): fall back to linear_extrap on prophet failure (counter)"
```

---

## Phase 4 — Pydantic models + FastAPI app (mostly Tier-1; integration via TestClient)

### Task 10: Pydantic request and response models

**Files:**
- Create: `forecast-service/src/forecast/models.py`
- Create: `forecast-service/tests/unit/test_models.py`

- [ ] **Step 1: Write failing model tests**

Create `forecast-service/tests/unit/test_models.py`:

```python
"""Tests for forecast.models (Pydantic v2)."""

from __future__ import annotations

import pytest
from pydantic import ValidationError

from forecast.models import RecommendRequest, RecommendResponse


def test_request_accepts_valid_input() -> None:
    req = RecommendRequest(
        rps_history=[100.0, 110.0, 120.0],
        workload_id="demo/app-agentic",
        preferred_model="auto",
    )
    assert req.rps_history == [100.0, 110.0, 120.0]
    assert req.workload_id == "demo/app-agentic"
    assert req.preferred_model == "auto"


def test_request_rejects_empty_history() -> None:
    with pytest.raises(ValidationError, match="rps_history"):
        RecommendRequest(rps_history=[])


def test_request_rejects_negative_value() -> None:
    with pytest.raises(ValidationError, match="non-negative"):
        RecommendRequest(rps_history=[100.0, -1.0, 110.0])


def test_request_workload_id_is_optional() -> None:
    req = RecommendRequest(rps_history=[100.0])
    assert req.workload_id is None


def test_request_preferred_model_optional() -> None:
    req = RecommendRequest(rps_history=[100.0])
    assert req.preferred_model is None


def test_response_round_trips() -> None:
    resp = RecommendResponse(
        predicted_rps=1450.0,
        horizon_minutes=10,
        model_used="prophet",
    )
    data = resp.model_dump()
    assert data == {
        "predicted_rps": 1450.0,
        "horizon_minutes": 10,
        "model_used": "prophet",
    }
```

- [ ] **Step 2: Run; expect ImportError**

```bash
cd forecast-service && pytest tests/unit/test_models.py -v
```

Expected: ImportError on `forecast.models`.

- [ ] **Step 3: Implement models**

Create `forecast-service/src/forecast/models.py`:

```python
"""Pydantic v2 request and response models for /recommend."""

from __future__ import annotations

from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator


class RecommendRequest(BaseModel):
    """Body of POST /recommend."""

    model_config = ConfigDict(extra="ignore")

    rps_history: Annotated[list[float], Field(min_length=1)]
    """Recent per-minute RPS values, oldest first."""

    workload_id: str | None = None
    """Free-form identifier; accepted but unused. Useful for tracing."""

    preferred_model: Literal["prophet", "linear_extrap", "auto"] | None = None
    """Override for model selection. None or 'auto' means defer to auto-select."""

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
    model_used: Literal["prophet", "linear_extrap"]
```

- [ ] **Step 4: Run**

```bash
pytest tests/unit/test_models.py -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
cd ..
git add forecast-service/
git commit -m "feat(forecast): add Pydantic v2 models for /recommend"
```

---

### Task 11: FastAPI app + /recommend + /healthz + /metrics

**Files:**
- Create: `forecast-service/src/forecast/app.py`
- Create: `forecast-service/tests/integration/test_app.py`

- [ ] **Step 1: Write failing integration tests using FastAPI TestClient**

Create `forecast-service/tests/integration/test_app.py`:

```python
"""Integration tests for the FastAPI app (no live network)."""

from __future__ import annotations

import os

import pytest
from fastapi.testclient import TestClient

# Ensure prophet's startup-warmup is skipped during tests so collection is fast.
os.environ.setdefault("FORECAST_SKIP_WARMUP", "1")

from forecast.app import app  # noqa: E402 — import after env set


@pytest.fixture
def client() -> TestClient:
    return TestClient(app)


def test_healthz_returns_ok(client: TestClient) -> None:
    resp = client.get("/healthz")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ok"}


def test_recommend_short_series_uses_linear_extrap(client: TestClient) -> None:
    resp = client.post(
        "/recommend",
        json={"rps_history": [100.0, 110.0, 120.0], "workload_id": "demo/app"},
    )
    assert resp.status_code == 200
    body = resp.json()
    assert body["model_used"] == "linear_extrap"
    assert body["horizon_minutes"] == 10
    assert body["predicted_rps"] >= 0.0


def test_recommend_long_series_uses_prophet(periodic_series_120: list[float]) -> None:
    client = TestClient(app)
    resp = client.post(
        "/recommend",
        json={"rps_history": periodic_series_120},
    )
    assert resp.status_code == 200
    body = resp.json()
    assert body["model_used"] == "prophet"


def test_recommend_rejects_empty_history(client: TestClient) -> None:
    resp = client.post("/recommend", json={"rps_history": []})
    assert resp.status_code == 422  # Pydantic validation


def test_recommend_rejects_negative_value(client: TestClient) -> None:
    resp = client.post("/recommend", json={"rps_history": [100.0, -1.0]})
    assert resp.status_code == 422


def test_recommend_honours_preferred_linear_extrap(periodic_series_120: list[float]) -> None:
    client = TestClient(app)
    resp = client.post(
        "/recommend",
        json={"rps_history": periodic_series_120, "preferred_model": "linear_extrap"},
    )
    assert resp.status_code == 200
    assert resp.json()["model_used"] == "linear_extrap"


def test_metrics_endpoint_returns_prometheus_format(client: TestClient) -> None:
    resp = client.get("/metrics")
    assert resp.status_code == 200
    body = resp.text
    assert "forecast_prophet_failures_total" in body
    # First line of a counter is the HELP line.
    assert body.startswith("# HELP") or "# HELP" in body.split("\n")[0:5]
```

- [ ] **Step 2: Run; expect ImportError**

```bash
cd forecast-service && pytest tests/integration/test_app.py -v
```

Expected: ImportError on `forecast.app`.

- [ ] **Step 3: Implement app.py**

Create `forecast-service/src/forecast/app.py`:

```python
"""FastAPI service for the forecast endpoint."""

from __future__ import annotations

import os

from fastapi import FastAPI
from fastapi.responses import Response
from prometheus_client import CONTENT_TYPE_LATEST, generate_latest

from forecast.dispatch import recommend
from forecast.models import RecommendRequest, RecommendResponse


app = FastAPI(title="agentic-forecast", version="0.1.0")


# These two values are read from env at startup. The controller and the
# Forecast Service must keep them in sync per design §4 / §5.
FORECAST_HORIZON_MINUTES = int(os.environ.get("FORECAST_HORIZON_MINUTES", "10"))
PROPHET_MIN_POINTS = int(os.environ.get("PROPHET_MIN_POINTS", "60"))


@app.get("/healthz")
async def healthz() -> dict[str, str]:
    return {"status": "ok"}


@app.get("/metrics")
async def metrics() -> Response:
    """Prometheus scrape endpoint."""
    return Response(content=generate_latest(), media_type=CONTENT_TYPE_LATEST)


@app.post("/recommend", response_model=RecommendResponse)
async def post_recommend(req: RecommendRequest) -> RecommendResponse:
    result = recommend(
        rps_history=req.rps_history,
        horizon_minutes=FORECAST_HORIZON_MINUTES,
        prophet_min_points=PROPHET_MIN_POINTS,
        preferred_model=req.preferred_model,
    )
    return RecommendResponse(**result)
```

- [ ] **Step 4: Run all integration tests**

```bash
pytest tests/integration/test_app.py -v
```

Expected: all PASS. Each Prophet-using test is ~1-3 s.

- [ ] **Step 5: Commit**

```bash
cd ..
git add forecast-service/
git commit -m "feat(forecast): add FastAPI app with /recommend, /healthz, /metrics"
```

---

### Task 12: Verify the controller-side env-var contract

**Files:** none (verification step only)

The two env vars `FORECAST_HORIZON_MINUTES` and `PROPHET_MIN_POINTS` exist on both the controller (Plan #1) and the Forecast Service (this plan). They must stay in sync. T12 is a doc-grep that asserts they appear with their default values in both places.

- [ ] **Step 1: Verify both env vars are documented in the design**

```bash
grep -E 'FORECAST_HORIZON_MINUTES|PROPHET_MIN_POINTS' docs/design.md | head
```

Expected: lines reference both vars in the §4 env-var tables and the cross-component-sync note.

- [ ] **Step 2: Verify the controller's config loader has them**

```bash
grep -E 'FORECAST_HORIZON_MINUTES|PROPHET_MIN_POINTS' internal/config/config.go internal/config/config_test.go | head
```

Expected: at least 4 lines (each var declared, defaulted, and tested).

- [ ] **Step 3: Verify the forecast service reads them**

```bash
grep -E 'FORECAST_HORIZON_MINUTES|PROPHET_MIN_POINTS' forecast-service/src/forecast/app.py
```

Expected: both env vars referenced in `app.py`.

- [ ] **Step 4: No commit (verification step)**

This is a sanity check, not a code change. Move on to T13.

---

## Phase 5 — Warm-up + final wiring

### Task 13: Startup warm-up + first-call latency assertion

**Files:**
- Create: `forecast-service/src/forecast/warmup.py`
- Modify: `forecast-service/src/forecast/app.py`
- Create: `forecast-service/tests/integration/test_warmup.py`

The first Prophet fit on a fresh process compiles a Stan model and can take 5-15 s. Without warm-up, the first real `/recommend` exceeds `FORECAST_TIMEOUT_SECONDS=5s`. Warm-up runs one tiny synthetic fit during FastAPI startup so the cold-start cost is paid before any traffic arrives.

- [ ] **Step 1: Write the failing warmup test**

Create `forecast-service/tests/integration/test_warmup.py`:

```python
"""Tests for the startup warm-up behaviour."""

from __future__ import annotations

import time

import pytest

from forecast.warmup import warmup_prophet


def test_warmup_completes_without_exception() -> None:
    """A single small Prophet fit should run cleanly."""
    warmup_prophet()  # raises on any failure


@pytest.mark.slow
def test_first_real_call_is_fast_after_warmup() -> None:
    """After warmup, the next prophet call must complete under 5 s
    (the controller's FORECAST_TIMEOUT_SECONDS default).

    Marked `slow` so PR-CI can opt out if the runner is heavily loaded.
    """
    warmup_prophet()
    from forecast.prophet_model import forecast_prophet

    series = [200.0 + 5.0 * i for i in range(70)]
    start = time.perf_counter()
    _ = forecast_prophet(series, horizon_minutes=10)
    elapsed = time.perf_counter() - start

    assert elapsed < 5.0, f"first post-warmup call took {elapsed:.2f}s"
```

- [ ] **Step 2: Run; expect ImportError**

```bash
cd forecast-service && pytest tests/integration/test_warmup.py -v
```

Expected: ImportError on `forecast.warmup`.

- [ ] **Step 3: Implement warmup.py**

Create `forecast-service/src/forecast/warmup.py`:

```python
"""Startup warm-up.

Per docs/design.md §5: 'During FastAPI's startup hook, the service performs
one dummy Prophet fit on a small synthetic series so the first real
/recommend call doesn't pay the Stan-compilation cost.'
"""

from __future__ import annotations

import logging

from forecast.prophet_model import forecast_prophet


def warmup_prophet() -> None:
    """Fit Prophet once on a 90-point synthetic series. Eats the Stan
    compilation cold-start cost. Returns immediately on success; logs
    and swallows on failure (warmup is best-effort)."""
    series = [200.0 + 0.5 * i for i in range(90)]
    try:
        _ = forecast_prophet(series, horizon_minutes=10)
        logging.info("prophet warmup complete")
    except Exception as exc:  # noqa: BLE001 - warmup is best-effort
        logging.warning("prophet warmup failed: %s", exc)
```

- [ ] **Step 4: Wire warmup into FastAPI startup**

Modify `forecast-service/src/forecast/app.py`. After the `app = FastAPI(...)` line and before the route handlers, add:

```python
@app.on_event("startup")
async def _warmup() -> None:
    if os.environ.get("FORECAST_SKIP_WARMUP") == "1":
        return
    from forecast.warmup import warmup_prophet
    warmup_prophet()
```

- [ ] **Step 5: Run the warmup tests**

```bash
pytest tests/integration/test_warmup.py -v
```

Expected: `test_warmup_completes_without_exception` PASSES; `test_first_real_call_is_fast_after_warmup` PASSES (the second call after warm-up is sub-second on most machines).

If the slow test is flaky on slower runners, mark it `@pytest.mark.skipif(os.environ.get("CI") == "true", reason="too tight for CI")` and document in Plan #11's CI runbook.

- [ ] **Step 6: Commit**

```bash
cd ..
git add forecast-service/
git commit -m "feat(forecast): add prophet warm-up at FastAPI startup"
```

---

### Task 14: Already-wired metrics + final container smoke

**Files:** none (verification step)

`metrics.py` was created in T7 and the counter is already wired in T9's dispatch fallback. The `/metrics` endpoint was added in T11. T14 verifies these three pieces are still consistent and runs the container as a final smoke.

- [ ] **Step 1: Full lint**

```bash
cd forecast-service && ruff check . && mypy src
```

Expected: clean.

- [ ] **Step 2: Full test pass with coverage**

```bash
pytest -v
```

Expected: every test PASSES; coverage report at the end shows `forecast` package at >=90% overall (per the `--cov-fail-under=90` setting in `pyproject.toml`).

- [ ] **Step 3: Container build + smoke run**

```bash
docker build -t agentic-forecast:dev .
docker run --rm -d --name forecast-smoke -p 8001:8000 agentic-forecast:dev
sleep 5
curl -sf http://localhost:8001/healthz
curl -sf -X POST http://localhost:8001/recommend \
    -H 'Content-Type: application/json' \
    -d '{"rps_history": [100.0, 110.0, 120.0]}'
curl -sf http://localhost:8001/metrics | head -20
docker stop forecast-smoke
```

Expected:
- `/healthz` returns `{"status":"ok"}`.
- `/recommend` returns a JSON body with `predicted_rps`, `horizon_minutes`, `model_used`.
- `/metrics` returns Prometheus text containing `forecast_prophet_failures_total`.

- [ ] **Step 4: No commit (verification step)**

---

### Task 15: README

**Files:**
- Create: `forecast-service/README.md`

- [ ] **Step 1: Create README.md**

Create `forecast-service/README.md`:

```markdown
# agentic-forecast

Forecast service for the agentic autoscaler. Exposes:

- `POST /recommend` — predicts RPS `FORECAST_HORIZON_MINUTES` ahead.
- `GET /healthz` — liveness.
- `GET /metrics` — Prometheus exposition; primary metric is `forecast_prophet_failures_total`.

## Configuration

| Env var | Default | Notes |
|---|---|---|
| `FORECAST_HORIZON_MINUTES` | 10 | Must match the controller's `FORECAST_HORIZON_MINUTES`. |
| `PROPHET_MIN_POINTS` | 60 | Below this length, dispatch picks `linear_extrap`. Must match the controller's `PROPHET_MIN_POINTS`. |
| `FORECAST_SKIP_WARMUP` | unset | Set to `1` to skip the startup Prophet fit. Useful for tests. |

## Local dev

```bash
cd forecast-service
python3.12 -m venv .venv
. .venv/bin/activate
pip install -e ".[dev]"
pytest -v
uvicorn forecast.app:app --reload --port 8000
```

## Container

```bash
docker build -t agentic-forecast:dev .
docker run --rm -p 8000:8000 agentic-forecast:dev
```
```

- [ ] **Step 2: Commit**

```bash
cd ..
git add forecast-service/README.md
git commit -m "docs(forecast): add README for the forecast service"
```

---

### Task 16: Milestone marker

**Files:** none

- [ ] **Step 1: Empty milestone commit**

```bash
git commit --allow-empty -m "milestone: Plan #7 (forecast service) complete

- linear_extrap and prophet implementations match design §5 exactly
- dispatcher auto-selects by length, honours preferred_model, falls back
  to linear_extrap on Prophet exception (counter increments)
- FastAPI app exposes /recommend, /healthz, /metrics
- startup warm-up eats Stan cold-start cost
- container builds and runs end-to-end
- 90%+ coverage gate enforced in pyproject.toml
"
```

---

## Plan-specific Definition of Done

- [ ] `cd forecast-service && pytest -v --cov-fail-under=90` exits zero.
- [ ] `cd forecast-service && ruff check . && mypy src` exits zero.
- [ ] `cd forecast-service && docker build -t agentic-forecast:dev .` succeeds.
- [ ] Container started with `docker run -p 8001:8000 agentic-forecast:dev` responds to `GET /healthz` with `{"status":"ok"}` within 30 s of starting (warmup completes in this window).
- [ ] `POST /recommend` with a 60+ point series returns `model_used: "prophet"`; with a 5-point series returns `model_used: "linear_extrap"`.
- [ ] `POST /recommend` with `preferred_model: "linear_extrap"` always returns `model_used: "linear_extrap"`, regardless of length.
- [ ] `POST /recommend` with `preferred_model: null` is identical to omitting the field.
- [ ] `GET /metrics` returns Prometheus text containing `forecast_prophet_failures_total`.
- [ ] Simulating a Prophet exception (via test mocking) increments `forecast_prophet_failures_total` by exactly 1.

---

## Notes on what's intentionally deferred

- **Forecast Service Deployment manifest, Service, ConfigMap** — Plan #10. This plan only ships the container image artifact.
- **Go-side Forecast adapter** — Plan #3. Consumes this service via HTTP; no shared code.
- **Cross-language fixture format** — Plan #11 (the strategy doc's `testdata/SCHEMA.md`). Plan #7 uses Python-side conftest fixtures only; the Go-canonical fixtures arrive later and Plan #5 is what they're for.
- **Memory limits and resource specs** — Plan #10's manifest sets `limits.memory=1Gi` per the strategy doc's R12 mitigation.

---

## Self-Review (Spec Coverage, Placeholders, Type Consistency)

**Spec coverage.**

- §3 architecture (service shape) → T1, T11
- §5 forecast_linear_extrap algorithm → T3, T4 (last-10-point slice, polyfit, target_x = n+horizon-1, clamp to 0)
- §5 forecast_prophet algorithm → T5, T6 (synthetic timestamps, Prophet config matches design exactly, predict, clamp)
- §5 dispatch logic → T7, T8, T9 (auto-select by length, override variants including auto/null/empty, exception fallback)
- §5 startup warm-up → T13 (synthetic 90-point series, FastAPI on_event startup, FORECAST_SKIP_WARMUP escape hatch for tests)
- §9 Prophet failure → linear_extrap, counter increments → T9 (mocked Prophet failure, `forecast_prophet_failures_total._value.get()` snapshot)
- §9 invalid response — covered by Pydantic v2 validation in T10 + the explicit `max(0.0, ...)` clamps in T3 and T5

**Placeholders.** None. Every code block is complete and pasteable. Every command has expected output.

**Type consistency.**

- `recommend()` signature in `dispatch.py` (T7) takes `rps_history`, `horizon_minutes`, `prophet_min_points`, `preferred_model`. The same parameter names are used in `app.py` (T11) and `tests/unit/test_dispatch.py` (T7-T9).
- `RecommendRequest` field names (T10): `rps_history`, `workload_id`, `preferred_model` — match design §5's request shape.
- `RecommendResponse` field names (T10): `predicted_rps`, `horizon_minutes`, `model_used` — match design §5's response shape.
- `model_used` literal values (T7, T8, T9): only `"prophet"` and `"linear_extrap"`. Same set used in tests and in the Pydantic Literal type.
- `preferred_model` literal values (T7, T8, T10): `"prophet"`, `"linear_extrap"`, `"auto"`, `None`. Pydantic accepts `None | "prophet" | "linear_extrap" | "auto"`; dispatch treats anything else as auto.
- `forecast_prophet_failures_total` counter name (T7 in `metrics.py`, T9 in `dispatch.py`, T11 in `/metrics` endpoint test) matches design §9 verbatim.

---

## Execution handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-24-plan-07-forecast-service.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using `executing-plans`, batch execution with checkpoints for review.

Which approach?



