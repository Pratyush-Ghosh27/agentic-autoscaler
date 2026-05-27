"""Forecast dispatcher.

Auto-selects between Prophet and linear extrapolation based on history
length, honours an optional preferred_model override, and falls back
to linear_extrap if Prophet raises (incrementing
forecast_prophet_failures_total).
"""

from __future__ import annotations

import logging
from typing import TYPE_CHECKING, Literal, TypedDict

from forecast.linear_extrap import forecast_linear_extrap
from forecast.metrics import forecast_prophet_failures_total
from forecast.prophet_model import forecast_prophet

if TYPE_CHECKING:
    from forecast.models import ContextPayload

ModelName = Literal["prophet", "linear_extrap", "gbdt_quantile"]


class RecommendResult(TypedDict):
    predicted_rps: float
    horizon_minutes: int
    model_used: ModelName


def recommend(
    rps_history: list[float],
    horizon_minutes: int,
    prophet_min_points: int,
    preferred_model: str | None = None,
    context: ContextPayload | None = None,
) -> RecommendResult:
    """Return the predicted RPS using the best available forecaster.

    Selection rules (per docs/design.md §5):
    1. If preferred_model is "prophet" or "linear_extrap", use it directly.
    2. Else if len(rps_history) >= prophet_min_points, attempt prophet
       (and fall through to linear_extrap on exception).
    3. Else use linear_extrap.

    "No override" values: None, "auto", "", or any unknown string.

    G10 (Phase 2): ``context`` is the cold-path-computed
    :class:`ContextPayload` (baseline_rps, peak_p95_rps,
    trend_24h_slope, hourly_profile + validity flag, plus current
    hour/minute UTC). Phase 2 forwards-without-consuming: each
    forecaster keeps its current signature, and we only log
    receipt. Phase 3 wires context into each forecaster's call.
    """
    if context is not None:
        logging.debug(
            "context forwarded: baseline=%d p95=%d trend_24h_slope=%.4f valid=%s",
            context.baseline_rps,
            context.peak_p95_rps,
            context.trend_24h_slope,
            context.hourly_profile_valid,
        )

    use_prophet = _should_use_prophet(
        rps_history=rps_history,
        prophet_min_points=prophet_min_points,
        preferred_model=preferred_model,
    )

    model_used: ModelName
    if use_prophet:
        try:
            predicted = forecast_prophet(
                rps_history, horizon_minutes, context=context
            )
            model_used = "prophet"
        except Exception as exc:  # noqa: BLE001 - any Prophet failure is a fallback trigger
            logging.warning("prophet failed, falling back to linear_extrap: %s", exc)
            forecast_prophet_failures_total.inc()
            predicted = forecast_linear_extrap(rps_history, horizon_minutes)
            model_used = "linear_extrap"
    else:
        predicted = forecast_linear_extrap(rps_history, horizon_minutes)
        model_used = "linear_extrap"

    return {
        "predicted_rps": predicted,
        "horizon_minutes": horizon_minutes,
        "model_used": model_used,
    }


def _should_use_prophet(
    rps_history: list[float],
    prophet_min_points: int,
    preferred_model: str | None,
) -> bool:
    """Pure selector — no side effects, no fitting."""
    if preferred_model == "prophet":
        return True
    if preferred_model == "linear_extrap":
        return False
    return len(rps_history) >= prophet_min_points
