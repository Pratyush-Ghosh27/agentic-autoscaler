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


# --- T6/T7/T8 shared helper --------------------------------------------------


def _ctx(*, trend: float = 0.0, p95: int = 1000) -> ContextPayload:  # noqa: F821
    """Build a minimal ContextPayload for linear_extrap context tests.

    Lazy-import keeps test collection cheap when only the no-context
    tests are run."""
    from forecast.models import ContextPayload

    return ContextPayload(
        baseline_rps=50,
        peak_p95_rps=p95,
        trend_24h_slope=trend,
        hourly_profile=[50] * 24,
        hourly_profile_valid=True,
        current_hour_utc=12,
        current_minute_utc=0,
    )


# --- T6: blend slope with context.trend_24h_slope (G15, F16) ----------------


def test_linear_extrap_blends_slope_with_trend_24h_slope(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """T6 (F16 / G15): m_blended = WEIGHT * m + (1 - WEIGHT) * trend_24h_slope.

    A flat recent window (m=0) blended at WEIGHT=0.5 with a long-term
    trend of +0.2 rps/min yields slope=0.1. Combined with T7's F31
    centroid recompute, intercept becomes
    ``y_bar - m_blended * x_bar = 100 - 0.1 * 4.5 = 99.55``, so at
    target_x = 19 the prediction is ``0.1 * 19 + 99.55 = 101.45``.
    """
    monkeypatch.setenv("LINEAR_EXTRAP_RECENT_WEIGHT", "0.5")
    monkeypatch.setenv("LINEAR_EXTRAP_WINDOW_MINUTES", "10")
    history = [100.0] * 10
    predicted = forecast_linear_extrap(
        history, horizon_minutes=10, context=_ctx(trend=0.2)
    )
    assert predicted == pytest.approx(101.45, abs=0.01)


def test_linear_extrap_ignores_trend_when_context_is_none() -> None:
    """T6 (G15): with no context, behaviour matches pre-Phase-3
    linear_extrap. Flat history -> flat prediction."""
    history = [100.0] * 10
    predicted = forecast_linear_extrap(history, horizon_minutes=5, context=None)
    assert predicted == pytest.approx(100.0, abs=1e-6)


# --- T7: re-anchor intercept at the data centroid after blend (F31) ----------


def test_linear_extrap_intercept_is_centroid_anchored_after_blend(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """T7 (F31 / G15): after blending the slope we must recompute the
    intercept so the line continues to pass through the data centroid
    ``(mean(x), mean(y))``. Without F31 the slope blend rotates the
    line around ``x=0`` instead, which biases predictions toward 0
    whenever the polyfit intercept is far from y_bar.

    Construction:
      history = [0, 0, 0, 0, 0, 100] over n=6.
      np.polyfit gives m_recent and an intercept that already lies on
      the centroid (OLS does that for us): mean(y) = 100/6 ≈ 16.667,
      mean(x) = 2.5.
      Blend at WEIGHT=0.0 with trend=0 sets the slope to 0.
      Without F31 the original polyfit intercept (negative, off the
      centroid) is reused -> predicted clamps to 0.
      With F31 the intercept is recomputed: b = y_bar - 0 * x_bar
      = 16.667 -> predicted = 0*10 + 16.667 = 16.667 at target_x = 10.
    """
    monkeypatch.setenv("LINEAR_EXTRAP_RECENT_WEIGHT", "0.0")
    monkeypatch.setenv("LINEAR_EXTRAP_WINDOW_MINUTES", "10")
    history = [0.0, 0.0, 0.0, 0.0, 0.0, 100.0]
    predicted = forecast_linear_extrap(
        history, horizon_minutes=5, context=_ctx(trend=0.0)
    )
    assert predicted == pytest.approx(16.6667, abs=0.01)


# --- T8: clip at peak_p95_rps * 1.5 (G15) -----------------------------------


def test_linear_extrap_clips_at_peak_p95_times_one_point_five(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """T8 (G15): a runaway short-window slope must be clipped at
    ``context.peak_p95_rps * 1.5``. With WEIGHT=1.0 the blend is a
    no-op so the raw recent slope drives the prediction; the only
    surviving safety net is the upper clip.

    The history is a 10-point ramp from 0 to 1000 (recent slope ~111).
    At horizon=10, target_x = 10 + 10 - 1 = 19. After T7's intercept
    recompute (y_bar=450, x_bar=4.5, slope=111.515...) the unclipped
    prediction is ~(111.5*19 - 111.5*4.5 + 450) = ~2068; the clip at
    peak_p95_rps=200 * 1.5 = 300 must bring it back to exactly 300.
    """
    monkeypatch.setenv("LINEAR_EXTRAP_RECENT_WEIGHT", "1.0")
    monkeypatch.setenv("LINEAR_EXTRAP_WINDOW_MINUTES", "10")
    history = [0.0, 100.0, 200.0, 300.0, 400.0, 500.0, 600.0, 700.0, 800.0, 1000.0]
    predicted = forecast_linear_extrap(
        history, horizon_minutes=10, context=_ctx(trend=0.0, p95=200)
    )
    assert predicted == pytest.approx(300.0, abs=0.01), (
        f"expected clipped to 300, got {predicted}"
    )


def test_linear_extrap_does_not_clip_when_context_is_none() -> None:
    """T8 (G15): without context, no peak_p95_rps is known, so no clip
    applies — back-compat with cold-start callers must be preserved."""
    history = [0.0, 100.0, 200.0, 300.0, 400.0, 500.0, 600.0, 700.0, 800.0, 1000.0]
    predicted = forecast_linear_extrap(history, horizon_minutes=10, context=None)
    assert predicted > 500.0, (
        "expected an unclipped large extrapolation when context is None"
    )


# --- Coverage for _window_minutes() / _recent_weight() defensive paths ------


@pytest.mark.parametrize(
    ("env_value", "kind"),
    [
        ("-3", "non-positive"),
        ("0", "zero"),
    ],
)
def test_linear_extrap_window_falls_back_on_non_positive_env(
    monkeypatch: pytest.MonkeyPatch,
    env_value: str,
    kind: str,
) -> None:
    """T5 defensive: a non-positive LINEAR_EXTRAP_WINDOW_MINUTES must
    fall back to the default of 10. Indirect proof: with the all-100
    flat 10-pt series the default selects all 10 points and predicts
    100. A broken helper would either crash or use the absurd value."""
    _ = kind
    monkeypatch.setenv("LINEAR_EXTRAP_WINDOW_MINUTES", env_value)
    predicted = forecast_linear_extrap([100.0] * 10, horizon_minutes=1)
    assert predicted == pytest.approx(100.0, abs=1e-6)


@pytest.mark.parametrize(
    ("env_value", "kind"),
    [
        ("not-a-float", "malformed"),
        ("-0.1", "negative"),
        ("1.1", "above-one"),
    ],
)
def test_linear_extrap_recent_weight_falls_back_on_bad_env(
    monkeypatch: pytest.MonkeyPatch,
    env_value: str,
    kind: str,
) -> None:
    """T6 defensive: LINEAR_EXTRAP_RECENT_WEIGHT outside [0, 1] or
    non-float falls back to the default (0.7). Indirect proof: with
    flat history (m=0) and trend=0 the prediction must equal 100
    regardless of any non-zero blend weight."""
    _ = kind
    monkeypatch.setenv("LINEAR_EXTRAP_RECENT_WEIGHT", env_value)
    history = [100.0] * 10
    predicted = forecast_linear_extrap(
        history, horizon_minutes=5, context=_ctx(trend=0.0)
    )
    assert predicted == pytest.approx(100.0, abs=1e-6)
