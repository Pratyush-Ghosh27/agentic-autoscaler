"""Tests for forecast.linear_extrap."""

from __future__ import annotations

import pytest

from forecast.linear_extrap import forecast_linear_extrap

pytestmark = pytest.mark.filterwarnings("ignore:divide by zero:RuntimeWarning")


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


# --- T5: window from LINEAR_EXTRAP_WINDOW_MINUTES env var (G15) --------------


def test_linear_extrap_uses_window_minutes_env_var(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """T5 (G15): when LINEAR_EXTRAP_WINDOW_MINUTES=5, only the last 5
    points drive the fit. We use a 25-point series whose last 5 are
    flat at 50 and whose preceding 20 imply slope=+1; the env override
    must select the flat tail."""
    history = [float(i) for i in range(20)] + [50.0] * 5
    monkeypatch.setenv("LINEAR_EXTRAP_WINDOW_MINUTES", "5")
    predicted = forecast_linear_extrap(history, horizon_minutes=5)
    assert predicted == pytest.approx(50.0, abs=1e-6)


def test_linear_extrap_window_defaults_to_10(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """T5: when LINEAR_EXTRAP_WINDOW_MINUTES is unset, the default
    window of 10 points must be used. With history = [0..19], the
    last 10 are [10, 11, ..., 19] which fit slope=1, intercept=10
    (relative x-axis 0..9; y_bar=14.5, x_bar=4.5). Target x = 9 + 1
    = 10 -> predicted = 1*10 + 10 = 20."""
    monkeypatch.delenv("LINEAR_EXTRAP_WINDOW_MINUTES", raising=False)
    history = [float(i) for i in range(20)]
    predicted = forecast_linear_extrap(history, horizon_minutes=1)
    assert predicted == pytest.approx(20.0, abs=1e-6)


def test_linear_extrap_invalid_window_env_falls_back_to_default(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """T5: a malformed LINEAR_EXTRAP_WINDOW_MINUTES (non-int or <=0)
    must not crash the forecaster. We treat it as "use the default 10"
    so a typo in the operator's ConfigMap can't take the hot path
    offline."""
    monkeypatch.setenv("LINEAR_EXTRAP_WINDOW_MINUTES", "not-an-int")
    history = [float(i) for i in range(20)]
    predicted = forecast_linear_extrap(history, horizon_minutes=1)
    assert predicted == pytest.approx(20.0, abs=1e-6)
