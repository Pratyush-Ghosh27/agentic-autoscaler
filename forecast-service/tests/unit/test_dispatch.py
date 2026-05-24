"""Tests for forecast.dispatch.recommend()."""

from __future__ import annotations

from unittest.mock import patch

import pytest

from forecast.dispatch import recommend
from forecast.metrics import forecast_prophet_failures_total


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


def test_preferred_prophet_overrides_short_history(short_series_5: list[float]) -> None:
    """preferred_model='prophet' must win even when auto-select would pick linear_extrap."""
    result = recommend(
        rps_history=short_series_5 * 12,  # 60 points so prophet doesn't reject
        horizon_minutes=10,
        prophet_min_points=1000,
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
    """None, 'auto', and '' all mean: defer to length-based auto-select."""
    result = recommend(
        rps_history=linear_series_30,
        horizon_minutes=10,
        prophet_min_points=20,
        preferred_model=override,
    )
    assert result["model_used"] == "prophet"


def test_unknown_preferred_value_treated_as_auto(linear_series_30: list[float]) -> None:
    result = recommend(
        rps_history=linear_series_30,
        horizon_minutes=10,
        prophet_min_points=20,
        preferred_model="experimental_xgboost",
    )
    assert result["model_used"] == "prophet"


def test_prophet_failure_falls_back_to_linear_extrap(
    linear_series_30: list[float],
) -> None:
    before = forecast_prophet_failures_total._value.get()
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
    """Explicit prophet override must still fall back rather than 5xx."""
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
