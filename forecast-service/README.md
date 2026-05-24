# agentic-forecast

Forecast service for the agentic autoscaler. Exposes:

- `POST /recommend` — predicts RPS `FORECAST_HORIZON_MINUTES` ahead.
- `GET /healthz` — liveness.
- `GET /metrics` — Prometheus exposition; primary metric is `forecast_prophet_failures_total`.

## Configuration

| Env var | Default | Notes |
|---|---|---|
| `FORECAST_HORIZON_MINUTES` | `10` | Must match the controller's `FORECAST_HORIZON_MINUTES`. |
| `PROPHET_MIN_POINTS` | `60` | Below this length, dispatch picks `linear_extrap`. Must match the controller's `PROPHET_MIN_POINTS`. |
| `FORECAST_SKIP_WARMUP` | unset | Set to `1` to skip the startup Prophet fit. Useful for tests. |

## Model selection

`POST /recommend` accepts `preferred_model`:

- `"prophet"` or `"linear_extrap"` — explicit override.
- `"auto"`, `null`, or omitted — auto-select by `len(rps_history) >= PROPHET_MIN_POINTS`.
- Any other value — treated as `auto` (forwards-compatible).

Prophet fit failures fall back to `linear_extrap` and increment
`forecast_prophet_failures_total`. The response shape never includes
errors; clients always get a `predicted_rps` value (clamped to
`max(0.0, ...)`).

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
