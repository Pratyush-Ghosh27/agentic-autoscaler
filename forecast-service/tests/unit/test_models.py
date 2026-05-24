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
