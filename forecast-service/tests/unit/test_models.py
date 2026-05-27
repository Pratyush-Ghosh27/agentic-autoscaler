"""Tests for forecast.models (Pydantic v2)."""

from __future__ import annotations

import pytest
from pydantic import ValidationError

from forecast.models import ContextPayload, RecommendRequest, RecommendResponse


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


def test_context_payload_round_trips() -> None:
    """G10: ContextPayload accepts the full v1alpha1.ContextFields shape
    plus current-time fields supplied by the controller."""
    ctx = ContextPayload(
        baseline_rps=50,
        peak_p95_rps=200,
        trend_24h_slope=0.5,
        hourly_profile=[10, 12, 14, 18, 22, 30, 50, 80, 100, 120, 140, 150,
                        150, 145, 140, 130, 110, 95, 80, 60, 40, 25, 15, 10],
        hourly_profile_valid=True,
        current_hour_utc=14,
        current_minute_utc=30,
    )
    assert ctx.baseline_rps == 50
    assert ctx.peak_p95_rps == 200
    assert ctx.trend_24h_slope == 0.5
    assert len(ctx.hourly_profile) == 24
    assert ctx.hourly_profile_valid is True
    assert ctx.current_hour_utc == 14
    assert ctx.current_minute_utc == 30


def test_context_payload_rejects_wrong_profile_length() -> None:
    """G10: HourlyProfile must be exactly 24 bins (one per UTC hour)."""
    with pytest.raises(ValidationError, match="hourly_profile"):
        ContextPayload(
            baseline_rps=50,
            peak_p95_rps=200,
            trend_24h_slope=0.0,
            hourly_profile=[10, 20, 30],
            hourly_profile_valid=True,
            current_hour_utc=0,
            current_minute_utc=0,
        )


def test_context_payload_rejects_out_of_range_hour() -> None:
    """G10: current_hour_utc must be 0..23."""
    with pytest.raises(ValidationError, match="current_hour_utc"):
        ContextPayload(
            baseline_rps=50,
            peak_p95_rps=200,
            trend_24h_slope=0.0,
            hourly_profile=[0] * 24,
            hourly_profile_valid=False,
            current_hour_utc=24,
            current_minute_utc=0,
        )


def test_context_payload_rejects_out_of_range_minute() -> None:
    """G10: current_minute_utc must be 0..59."""
    with pytest.raises(ValidationError, match="current_minute_utc"):
        ContextPayload(
            baseline_rps=50,
            peak_p95_rps=200,
            trend_24h_slope=0.0,
            hourly_profile=[0] * 24,
            hourly_profile_valid=False,
            current_hour_utc=0,
            current_minute_utc=60,
        )


def test_request_context_optional() -> None:
    """G10: context is optional — pre-classified or skip-context omits it."""
    req = RecommendRequest(rps_history=[100.0])
    assert req.context is None


def test_request_accepts_context() -> None:
    """G10: RecommendRequest accepts a populated context."""
    req = RecommendRequest(
        rps_history=[100.0, 110.0, 120.0],
        context={
            "baseline_rps": 50,
            "peak_p95_rps": 200,
            "trend_24h_slope": 0.5,
            "hourly_profile": [0] * 24,
            "hourly_profile_valid": False,
            "current_hour_utc": 9,
            "current_minute_utc": 15,
        },
    )
    assert req.context is not None
    assert req.context.baseline_rps == 50
    assert req.context.current_hour_utc == 9


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


def test_request_accepts_gbdt_quantile_preferred_model() -> None:
    """T2 (G12): the gbdt_quantile pin must be a valid preferred_model
    value at the schema layer, so users can opt in via the CRD."""
    req = RecommendRequest(
        horizon_minutes=10,
        rps_history=[1.0, 2.0, 3.0],
        preferred_model="gbdt_quantile",
    )
    assert req.preferred_model == "gbdt_quantile"


def test_response_round_trips_with_gbdt_quantile_model_used() -> None:
    """T2 (G12): the response Literal must accept gbdt_quantile so the
    dispatcher (T12) can advertise that the model was actually used."""
    resp = RecommendResponse(
        predicted_rps=1450.0,
        horizon_minutes=10,
        model_used="gbdt_quantile",
    )
    assert resp.model_used == "gbdt_quantile"
