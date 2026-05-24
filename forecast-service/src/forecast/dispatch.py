"""Forecast dispatcher.

Auto-selects between Prophet and linear extrapolation based on history
length, honours an optional preferred_model override, and falls back
to linear_extrap if Prophet raises (incrementing
forecast_prophet_failures_total).
"""

from __future__ import annotations

import logging
from typing import Literal, TypedDict

from forecast.linear_extrap import forecast_linear_extrap
from forecast.metrics import forecast_prophet_failures_total
from forecast.prophet_model import forecast_prophet

ModelName = Literal["prophet", "linear_extrap"]


class RecommendResult(TypedDict):
    predicted_rps: float
    horizon_minutes: int
    model_used: ModelName


def recommend(
    rps_history: list[float],
    horizon_minutes: int,
    prophet_min_points: int,
    preferred_model: str | None = None,
) -> RecommendResult:
    """Return the predicted RPS using the best available forecaster.

    Selection rules (per docs/design.md §5):
    1. If preferred_model is "prophet" or "linear_extrap", use it directly.
    2. Else if len(rps_history) >= prophet_min_points, attempt prophet
       (and fall through to linear_extrap on exception).
    3. Else use linear_extrap.

    "No override" values: None, "auto", "", or any unknown string.
    """
    use_prophet = _should_use_prophet(
        rps_history=rps_history,
        prophet_min_points=prophet_min_points,
        preferred_model=preferred_model,
    )

    model_used: ModelName
    if use_prophet:
        try:
            predicted = forecast_prophet(rps_history, horizon_minutes)
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
