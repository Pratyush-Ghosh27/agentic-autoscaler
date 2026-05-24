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
    assert result < 10000.0


def test_prophet_on_flat_series_returns_around_flat_value(flat_series_30: list[float]) -> None:
    result = forecast_prophet(flat_series_30, horizon_minutes=10)
    assert 140.0 <= result <= 260.0


def test_prophet_clamps_negative_predictions_to_zero() -> None:
    series = [100.0 - 5.0 * i for i in range(20)]
    result = forecast_prophet(series, horizon_minutes=10)
    assert result >= 0.0


def test_prophet_raises_on_empty_history() -> None:
    with pytest.raises(ValueError, match="empty"):
        forecast_prophet([], horizon_minutes=10)


def test_prophet_raises_on_negative_horizon() -> None:
    with pytest.raises(ValueError, match="horizon"):
        forecast_prophet([100.0, 110.0], horizon_minutes=-1)
