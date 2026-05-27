"""Tests for forecast.dispatch.recommend()."""

from __future__ import annotations

from unittest.mock import patch

import pytest

from forecast.dispatch import recommend
from forecast.metrics import (
    forecast_dispatch_total,
    forecast_prophet_failures_total,
)


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


# -----------------------------------------------------------------------
# G10: Forecast Service accepts the cold-path-computed Context block.
#
# Phase 2's contract is "forward without consuming": dispatch() accepts
# the validated ContextPayload, logs receipt, and proceeds. Each
# forecaster gets its context-aware variant in Phase 3 (this is a
# pure plumbing milestone).
# -----------------------------------------------------------------------


def test_recommend_accepts_context(linear_series_30: list[float]) -> None:
    """A valid ContextPayload flows through recommend() and the result
    is identical (model selection unchanged). The point of this test
    is the absence of a TypeError on the kwarg, plus that the model
    behaviour is unaffected — Phase 3 is where context starts to bite."""
    from forecast.models import ContextPayload

    ctx = ContextPayload(
        baseline_rps=50,
        peak_p95_rps=200,
        trend_24h_slope=0.5,
        hourly_profile=[10] * 24,
        hourly_profile_valid=True,
        current_hour_utc=14,
        current_minute_utc=30,
    )
    result = recommend(
        rps_history=linear_series_30,
        horizon_minutes=10,
        prophet_min_points=30,
        preferred_model=None,
        context=ctx,
    )
    assert result["predicted_rps"] >= 0
    assert result["model_used"] in ("prophet", "linear_extrap")


def test_recommend_accepts_none_context(linear_series_30: list[float]) -> None:
    """None context is the cold-start signal and must not be a TypeError."""
    result = recommend(
        rps_history=linear_series_30,
        horizon_minutes=10,
        prophet_min_points=30,
        preferred_model=None,
        context=None,
    )
    assert result["predicted_rps"] >= 0


def test_recommend_context_does_not_change_selection(
    linear_series_30: list[float],
) -> None:
    """In Phase 2 context is forwarded but not consumed; the model
    chosen with vs. without context must be identical."""
    from forecast.models import ContextPayload

    ctx = ContextPayload(
        baseline_rps=50,
        peak_p95_rps=200,
        trend_24h_slope=0.5,
        hourly_profile=[10] * 24,
        hourly_profile_valid=True,
        current_hour_utc=14,
        current_minute_utc=30,
    )
    without = recommend(
        rps_history=linear_series_30,
        horizon_minutes=10,
        prophet_min_points=30,
    )
    with_ctx = recommend(
        rps_history=linear_series_30,
        horizon_minutes=10,
        prophet_min_points=30,
        context=ctx,
    )
    assert without["model_used"] == with_ctx["model_used"]
    # predicted_rps may differ slightly across runs due to Prophet's
    # internal MCMC; assert only on the model_used contract here.


# -----------------------------------------------------------------------
# T12 (G12 / F22): gbdt_quantile is routable + auto-mode never picks it
# -----------------------------------------------------------------------


def _gbdt_ctx():  # type: ignore[no-untyped-def]
    """Shared ContextPayload for the gbdt_quantile dispatch tests."""
    from forecast.models import ContextPayload

    return ContextPayload(
        baseline_rps=50,
        peak_p95_rps=200,
        trend_24h_slope=0.0,
        hourly_profile=[50] * 24,
        hourly_profile_valid=True,
        current_hour_utc=12,
        current_minute_utc=0,
    )


@pytest.mark.slow
def test_dispatch_routes_gbdt_quantile_when_preferred() -> None:
    """T12 (G12): ``preferred_model='gbdt_quantile'`` MUST route through
    the GBDT forecaster, not Prophet or linear_extrap. We use 50 flat
    samples so the gbdt length gate (>=GBDT_MIN_POINTS+horizon=40)
    passes."""
    history = [50.0] * 50
    result = recommend(
        rps_history=history,
        horizon_minutes=10,
        prophet_min_points=30,
        preferred_model="gbdt_quantile",
        context=_gbdt_ctx(),
    )
    assert result["model_used"] == "gbdt_quantile"
    assert result["predicted_rps"] >= 0.0


def test_dispatch_auto_never_picks_gbdt_quantile_across_history_sizes(
    short_series_5: list[float],
) -> None:
    """T12 (F22): the F22 invariant — ``preferred_model in
    {None, 'auto', '', unknown}`` MUST NEVER select gbdt_quantile,
    regardless of history length. Sweeping a range of sizes that
    cross both auto-prophet and short-history-linear thresholds
    proves the structural enforcement is not just a happy-path
    coincidence."""
    base = short_series_5  # length 5
    for n_history in (5, 30, 60, 240):
        history = (base * ((n_history // len(base)) + 1))[:n_history]
        for pref in (None, "auto", "", "experimental_xgboost"):
            result = recommend(
                rps_history=history,
                horizon_minutes=10,
                prophet_min_points=30,
                preferred_model=pref,
                context=_gbdt_ctx(),
            )
            assert result["model_used"] != "gbdt_quantile", (
                f"auto/None/'' selected gbdt_quantile at "
                f"n={n_history} pref={pref!r} — F22 invariant violated"
            )


# -----------------------------------------------------------------------
# Plan 18: forecast_dispatch_total counter is incremented on every
# successful recommend() call, labelled by the resolved model_used.
# The nightly E2E asserts on this counter to lock in the gbdt_quantile
# path (see test/e2e/assertions-gbdt.sh).
# -----------------------------------------------------------------------


def test_recommend_increments_forecast_dispatch_total_per_call(
    linear_series_30: list[float],
) -> None:
    """Every successful dispatch increments the counter labelled by the
    resolved model_used. The auto branch with a 30-point history at
    prophet_min_points=30 picks prophet, so prophet's child increments by
    exactly one; other labels are untouched."""
    before_prophet = forecast_dispatch_total.labels(model_used="prophet")._value.get()
    before_linear = forecast_dispatch_total.labels(model_used="linear_extrap")._value.get()

    result = recommend(
        rps_history=linear_series_30,
        horizon_minutes=10,
        prophet_min_points=30,
    )
    assert result["model_used"] == "prophet"

    after_prophet = forecast_dispatch_total.labels(model_used="prophet")._value.get()
    after_linear = forecast_dispatch_total.labels(model_used="linear_extrap")._value.get()
    assert after_prophet == before_prophet + 1, (
        f"prophet child must increment by exactly 1; before={before_prophet} after={after_prophet}"
    )
    assert after_linear == before_linear, (
        "linear_extrap child MUST be untouched when prophet was the resolved model"
    )


def test_recommend_increments_linear_extrap_child_on_prophet_failure_fallback(
    linear_series_30: list[float],
) -> None:
    """When prophet raises and dispatch falls back to linear_extrap, the
    counter increments under model_used='linear_extrap' (the *resolved*
    model after fallback), not under 'prophet'. This is the contract the
    nightly assertion relies on: the labelled count reflects what
    actually served the request."""
    before_prophet = forecast_dispatch_total.labels(model_used="prophet")._value.get()
    before_linear = forecast_dispatch_total.labels(model_used="linear_extrap")._value.get()

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

    after_prophet = forecast_dispatch_total.labels(model_used="prophet")._value.get()
    after_linear = forecast_dispatch_total.labels(model_used="linear_extrap")._value.get()
    assert after_prophet == before_prophet, "prophet MUST NOT increment on fallback"
    assert after_linear == before_linear + 1, (
        f"linear_extrap MUST increment exactly once on prophet→linear fallback; "
        f"before={before_linear} after={after_linear}"
    )


def test_recommend_increments_gbdt_quantile_child_on_explicit_route() -> None:
    """T12/Plan 18 lock-in: when preferred_model='gbdt_quantile' wins and
    the GBDT forecaster succeeds, the counter increments under
    'gbdt_quantile'. The nightly E2E uses this exact label to confirm
    the GBDT path was actually exercised."""
    before = forecast_dispatch_total.labels(model_used="gbdt_quantile")._value.get()

    history = [50.0] * 50
    from forecast.models import ContextPayload

    ctx = ContextPayload(
        baseline_rps=50,
        peak_p95_rps=200,
        trend_24h_slope=0.0,
        hourly_profile=[50] * 24,
        hourly_profile_valid=True,
        current_hour_utc=12,
        current_minute_utc=0,
    )
    result = recommend(
        rps_history=history,
        horizon_minutes=10,
        prophet_min_points=30,
        preferred_model="gbdt_quantile",
        context=ctx,
    )
    assert result["model_used"] == "gbdt_quantile"

    after = forecast_dispatch_total.labels(model_used="gbdt_quantile")._value.get()
    assert after == before + 1, (
        f"gbdt_quantile child must increment by exactly 1 on explicit route; "
        f"before={before} after={after}"
    )


def test_dispatch_gbdt_quantile_falls_back_when_history_too_short() -> None:
    """T12 (G12): when gbdt_quantile raises (short history is the
    most likely real-world cause), dispatch falls back to
    linear_extrap so the hot path never 5xx-es from a model issue —
    mirrors the Prophet failure path."""
    history = [50.0] * 5
    result = recommend(
        rps_history=history,
        horizon_minutes=10,
        prophet_min_points=30,
        preferred_model="gbdt_quantile",
        context=_gbdt_ctx(),
    )
    assert result["model_used"] == "linear_extrap"
