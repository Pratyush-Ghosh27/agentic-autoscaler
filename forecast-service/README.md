# agentic-forecast

Forecast service for the agentic autoscaler. Exposes:

- `POST /recommend` — predicts RPS `FORECAST_HORIZON_MINUTES` ahead.
- `GET /healthz` — liveness.
- `GET /metrics` — Prometheus exposition.

## Metrics

| Metric | Type | Labels | Purpose |
|---|---|---|---|
| `forecast_dispatch_total` | Counter | `model_used` | Cumulative count of successful `/recommend` dispatches, labelled by the **resolved** model (post-fallback, so a `prophet → linear_extrap` fallback increments under `linear_extrap`). The nightly E2E asserts on `model_used="gbdt_quantile" > 0` to lock in the gbdt path (Plan 18 — see `test/e2e/assertions-gbdt.sh`). |
| `forecast_prophet_failures_total` | Counter | — | Number of times Prophet raised during `/recommend`; dispatcher fell back to `linear_extrap`. |

## Configuration

| Env var | Default | Notes |
|---|---|---|
| `FORECAST_HORIZON_MINUTES` | `10` | Forecast horizon. The controller learns this from each `/recommend` response's `horizon_minutes` field — no controller-side env var needed (v2 change). |
| `PROPHET_MIN_POINTS` | `30` | Below this length, `auto` mode picks `linear_extrap`. v2 default lowered from 60 so Prophet engages halfway through the warm-up. **Forecast Service-only** in v2 (controller no longer reads it). |
| `GBDT_MIN_POINTS` | `30` | Below this length, `gbdt_quantile` falls back to `linear_extrap`. Mirrors `PROPHET_MIN_POINTS`. |
| `GBDT_QUANTILE` | `0.90` | Upper-quantile prediction target. p90 = "scale for a worse-than-typical burst." |
| `PROPHET_USE_HOURLY_REGRESSOR` | `true` | When true and `context.hourly_profile_valid == true`, Prophet adds `hour_baseline` as an external regressor. |
| `LINEAR_EXTRAP_RECENT_WEIGHT` | `0.7` | Blend weight `α` on the recent slope; trend gets `1 − α`. Pins polarity so future tuning can't silently reverse the blend. |
| `LINEAR_EXTRAP_WINDOW_MINUTES` | `10` | Window over which the recent slope is computed. |
| `HOURLY_PROFILE_MIN_HOURS` | `12` | Distinct UTC hours of context history required before `hourly_profile_valid` is true. |
| `FORECAST_SKIP_WARMUP` | unset | Set to `1` to skip the startup Prophet fit. Useful for tests. |

## Model selection

`POST /recommend` accepts a `preferred_model` field (mirrors the CR's `spec.preferredForecaster`):

- `"prophet"` — explicit Prophet route. Falls back to `linear_extrap` on Prophet exception.
- `"linear_extrap"` — explicit linear-extrapolation route.
- `"gbdt_quantile"` — explicit LightGBM quantile route (v2 addition). Falls back to `linear_extrap` on any exception (commonly: `len(rps_history) < GBDT_MIN_POINTS`).
- `"auto"`, `null`, or omitted — auto-select by `len(rps_history) >= PROPHET_MIN_POINTS`. **F22 invariant: `auto` mode never selects `gbdt_quantile`** — that path is opt-in only. (See `forecast-service/tests/unit/test_dispatch.py::test_dispatch_auto_never_picks_gbdt_quantile_across_history_sizes`.)
- Any other value — treated as `auto` (forwards-compatible).

The dispatcher records the **resolved** model (post-fallback) in `forecast_dispatch_total{model_used}`, so the labelled count reflects what actually served the request.

## Optional `context` payload (v2)

`POST /recommend` accepts an optional `context` block (5 fields) populated by the controller's ClassifierWorker:

```json
{
  "rps_history": [...],
  "horizon_minutes": 10,
  "preferred_model": "auto",
  "context": {
    "baseline_rps": 50,
    "peak_p95_rps": 200,
    "trend_24h_slope": 0.5,
    "hourly_profile": [10, 12, ..., /* 24 entries */],
    "hourly_profile_valid": true,
    "current_hour_utc": 14,
    "current_minute_utc": 30
  }
}
```

When present, each forecaster uses it differently:

- **Prophet** anchors `ds[-1]` to the context's UTC hour+minute (F3a/F17) and adds `hour_baseline` as a regressor when valid (G14).
- **`linear_extrap`** blends the recent slope with `trend_24h_slope`, recomputes the intercept around the centroid, and clips at `peak_p95_rps × 1.5` (G15).
- **`gbdt_quantile`** uses lag/hour-baseline features and a p95 cap (G12).

`context` is optional — passing `null` (or omitting it) is valid and triggers context-free behaviour, matching v1.

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

The image pre-imports Prophet during build so the runtime container
only pays Stan's first-fit cost during the FastAPI startup hook.
