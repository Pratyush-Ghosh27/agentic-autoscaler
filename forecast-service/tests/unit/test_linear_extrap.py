"""Tests for forecast.linear_extrap."""

from __future__ import annotations

import pytest

from forecast.linear_extrap import forecast_linear_extrap


def test_perfect_linear_series_extrapolates_correctly(linear_series_30: list[float]) -> None:
    """y = 100 + 2x, last point at x=29 (y=158).

    The implementation slices the last 10 points (original x=20..29) and
    uses a relative x-axis 0..9, then extrapolates to relative
    target_x = n + horizon - 1 = 19, which corresponds to original x=39.
    Therefore predicted = 100 + 2 * 39 = 178 (i.e., "10 minutes past
    the last point on a perfect line of slope 2").
    """
    result = forecast_linear_extrap(linear_series_30, horizon_minutes=10)
    assert result == pytest.approx(178.0, abs=0.01)


def test_flat_series_returns_flat_value(flat_series_30: list[float]) -> None:
    result = forecast_linear_extrap(flat_series_30, horizon_minutes=10)
    assert result == pytest.approx(200.0, abs=0.01)


def test_descending_series_clamps_to_zero() -> None:
    series = [100.0, 90.0, 80.0, 70.0, 60.0, 50.0, 40.0, 30.0, 20.0, 10.0]
    result = forecast_linear_extrap(series, horizon_minutes=10)
    assert result == pytest.approx(0.0)


def test_single_point_returns_that_point() -> None:
    result = forecast_linear_extrap([42.5], horizon_minutes=10)
    assert result == pytest.approx(42.5)


def test_empty_series_raises() -> None:
    with pytest.raises(ValueError, match="empty"):
        forecast_linear_extrap([], horizon_minutes=10)


def test_horizon_zero_extrapolates_to_last_index() -> None:
    """horizon=0 should pin the prediction to x=n-1 (i.e., the last point on the line)."""
    series = [100.0 + 2.0 * i for i in range(10)]
    result = forecast_linear_extrap(series, horizon_minutes=0)
    assert result == pytest.approx(118.0, abs=0.01)


def test_only_last_10_points_used() -> None:
    series = [10.0 * i for i in range(20)] + [1000.0] * 10
    result = forecast_linear_extrap(series, horizon_minutes=10)
    assert result == pytest.approx(1000.0, abs=0.01)
