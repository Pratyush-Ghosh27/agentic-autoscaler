"""Integration tests for the FastAPI app (no live network)."""

from __future__ import annotations

import os

os.environ.setdefault("FORECAST_SKIP_WARMUP", "1")

import pytest
from fastapi.testclient import TestClient

from forecast.app import app  # noqa: E402


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
    assert resp.status_code == 422


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


def test_recommend_endpoint_accepts_context(periodic_series_120: list[float]) -> None:
    """G10: the /recommend endpoint accepts a `context` block and
    returns a 200 with the same shape as a context-less request.
    Phase 2 ships forwarding only — Phase 3 will prove that
    context_aware vs. context_less responses differ for periodic
    workloads."""
    client = TestClient(app)
    payload = {
        "rps_history": periodic_series_120,
        "context": {
            "baseline_rps": 50,
            "peak_p95_rps": 200,
            "trend_24h_slope": 0.5,
            "hourly_profile": [10] * 24,
            "hourly_profile_valid": True,
            "current_hour_utc": 14,
            "current_minute_utc": 30,
        },
    }
    resp = client.post("/recommend", json=payload)
    assert resp.status_code == 200, resp.text
    body = resp.json()
    assert body["predicted_rps"] >= 0
    assert body["model_used"] in ("prophet", "linear_extrap")


def test_recommend_endpoint_rejects_malformed_context(
    periodic_series_120: list[float],
) -> None:
    """A short hourly_profile is a 422 from FastAPI (Pydantic catches
    the constraint violation in app.models)."""
    client = TestClient(app)
    payload = {
        "rps_history": periodic_series_120,
        "context": {
            "baseline_rps": 50,
            "peak_p95_rps": 200,
            "trend_24h_slope": 0.5,
            "hourly_profile": [10] * 5,
            "hourly_profile_valid": True,
            "current_hour_utc": 14,
            "current_minute_utc": 30,
        },
    }
    resp = client.post("/recommend", json=payload)
    assert resp.status_code == 422


@pytest.mark.slow
def test_recommend_endpoint_routes_gbdt_quantile_when_preferred(
    client: TestClient,
) -> None:
    """T14 (G12) end-to-end: POST /recommend with preferred_model
    "gbdt_quantile" and sufficient history (>=GBDT_MIN_POINTS +
    horizon = 40 by default) returns model_used="gbdt_quantile" with
    a finite, non-negative predicted_rps. This pins the full stack:
    schema accepts the value (T2), dispatcher routes it (T12),
    gbdt_model fits + predicts (T10), and the cap fires (T11)."""
    body = {
        "rps_history": [50.0] * 50,
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
    resp = client.post("/recommend", json=body)
    assert resp.status_code == 200, resp.text
    payload = resp.json()
    assert payload["model_used"] == "gbdt_quantile"
    assert payload["predicted_rps"] >= 0.0


def test_recommend_endpoint_auto_never_returns_gbdt_quantile(
    client: TestClient,
) -> None:
    """T14 (F22) end-to-end: even with a generous history and a
    context that would make gbdt_quantile plausible, preferred_model
    "auto" must NEVER return model_used="gbdt_quantile". This is the
    F22 invariant pinned at the wire level — the only way to reach
    gbdt_quantile is via an explicit pin."""
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
    resp = client.post("/recommend", json=body)
    assert resp.status_code == 200, resp.text
    payload = resp.json()
    assert payload["model_used"] != "gbdt_quantile", (
        f"F22 violated: auto mode returned gbdt_quantile "
        f"(payload={payload})"
    )
    assert payload["model_used"] in ("prophet", "linear_extrap")


def test_v2_env_vars_have_defaults() -> None:
    """G21, F24: every v2 env var has a sensible default so existing
    operators upgrade without setting anything new. Pinning these as
    module attributes lets the dispatch layer read them once and avoids
    scattering os.environ.get calls across the codebase."""
    from forecast import app as app_mod

    assert app_mod.FORECAST_HORIZON_MINUTES == 10
    assert app_mod.PROPHET_MIN_POINTS == 30, (
        "F2a-revisited: lowered from 60 to 30 (Prophet self-gates short histories)"
    )
    assert app_mod.LINEAR_EXTRAP_RECENT_WEIGHT == 0.7
    assert app_mod.LINEAR_EXTRAP_WINDOW_MINUTES == 10
    assert app_mod.GBDT_QUANTILE == 0.90
    assert app_mod.GBDT_MIN_POINTS == 30, "F24: configurable for ops tuning"
    assert app_mod.PROPHET_USE_HOURLY_REGRESSOR is True
    assert app_mod.HOURLY_PROFILE_MIN_HOURS == 12, (
        "G11: mirrored on the service for context validation"
    )
