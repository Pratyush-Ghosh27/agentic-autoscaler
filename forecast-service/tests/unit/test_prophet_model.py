"""Tests for forecast.prophet_model.

Prophet is real-fitting in these tests (no mocks). Synthetic periodic
series are short (60-120 points) so fits complete in <1 s each.

Tests added in Plan 14 T3/T4 that fit Prophet are marked
``@pytest.mark.slow`` so they can be skipped in the inner TDD loop
with ``pytest -m 'not slow'``.
"""

from __future__ import annotations

import pandas as pd
import pytest

from forecast.prophet_model import build_anchored_timestamps, forecast_prophet


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


def test_build_anchored_timestamps_lands_last_sample_on_request_clock() -> None:
    """T3 (F3a / F17): when the caller supplies (current_hour_utc=14,
    current_minute_utc=30), the synthetic last ds must have
    hour=14 and minute=30, independent of the service's wall clock."""
    timestamps = build_anchored_timestamps(
        n=60, current_hour_utc=14, current_minute_utc=30
    )
    last = timestamps[-1]
    assert last.hour == 14, f"last.hour={last.hour}, want 14"
    assert last.minute == 30, f"last.minute={last.minute}, want 30"
    assert (last - timestamps[0]) == pd.Timedelta(minutes=59)


def test_build_anchored_timestamps_falls_back_to_wall_clock_when_context_absent() -> None:
    """T3: when either hour or minute is None, the helper must defer
    to the service's wall clock — never raise — so cold-start callers
    that have no ContextPayload yet still get a usable ds column."""
    timestamps = build_anchored_timestamps(n=30, current_hour_utc=None, current_minute_utc=None)
    assert len(timestamps) == 30
    assert (timestamps[-1] - timestamps[0]) == pd.Timedelta(minutes=29)


@pytest.mark.slow
def test_prophet_accepts_context_kwarg_and_does_not_regress(
    flat_series_30: list[float],
) -> None:
    """T3: ``forecast_prophet`` must accept a ``context`` keyword
    argument and produce a finite, non-negative prediction when
    context is None (legacy callers must keep working)."""
    result = forecast_prophet(flat_series_30, horizon_minutes=5, context=None)
    assert result >= 0.0
    assert result < 10000.0
