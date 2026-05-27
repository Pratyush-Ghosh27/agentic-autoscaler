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


# --- T4: hourly_regressor on/off behaviour (G14) -----------------------------


def _ctx_with_profile_validity(*, valid: bool) -> ContextPayload:  # noqa: F821
    """Helper that builds a minimally-populated ContextPayload whose
    only knob is ``hourly_profile_valid``. Imported lazily to keep
    test collection cheap when Prophet tests are skipped."""
    from forecast.models import ContextPayload

    return ContextPayload(
        baseline_rps=50,
        peak_p95_rps=200,
        trend_24h_slope=0.0,
        hourly_profile=[50] * 24,
        hourly_profile_valid=valid,
        current_hour_utc=12,
        current_minute_utc=0,
    )


@pytest.mark.slow
def test_prophet_skips_hour_regressor_when_profile_invalid(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """T4 (G14): with PROPHET_USE_HOURLY_REGRESSOR=true *but* the
    incoming ContextPayload reports hourly_profile_valid=False, the
    regressor must be skipped (soft fallback). Behaviour is asserted
    via "does not raise" because Prophet would otherwise complain
    that an added regressor lacks values."""
    monkeypatch.setenv("PROPHET_USE_HOURLY_REGRESSOR", "true")
    history = [10.0] * 30
    predicted = forecast_prophet(
        history, horizon_minutes=5, context=_ctx_with_profile_validity(valid=False)
    )
    assert predicted >= 0.0


@pytest.mark.slow
def test_prophet_skips_hour_regressor_when_toggle_off(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """T4 (G14): PROPHET_USE_HOURLY_REGRESSOR=false suppresses the
    regressor even when the profile is valid. Same "does not raise"
    contract — we don't assert numeric movement here because Prophet
    runs are noisy on flat data; T14 covers end-to-end signal."""
    monkeypatch.setenv("PROPHET_USE_HOURLY_REGRESSOR", "false")
    history = [10.0] * 30
    predicted = forecast_prophet(
        history, horizon_minutes=5, context=_ctx_with_profile_validity(valid=True)
    )
    assert predicted >= 0.0
