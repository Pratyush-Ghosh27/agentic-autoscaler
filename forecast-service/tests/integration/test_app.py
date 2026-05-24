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
