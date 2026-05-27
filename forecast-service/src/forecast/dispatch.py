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
from forecast.metrics import (
    forecast_dispatch_total,
    forecast_prophet_failures_total,
)
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

    Selection rules (per docs/design_v2.md §5):

    1. If ``preferred_model == "gbdt_quantile"``, route through the
       LightGBM quantile regressor (fall back to ``linear_extrap`` on
       any exception).
    2. Else if ``preferred_model in {"prophet", "linear_extrap"}``,
       use it directly (Prophet falls back to ``linear_extrap`` on
       exception).
    3. Else (``preferred_model`` is None / ``"auto"`` / ``""`` /
       unknown): if ``len(rps_history) >= prophet_min_points`` try
       Prophet (fall back to ``linear_extrap`` on exception); else
       use ``linear_extrap`` directly.

    F22 invariant: the auto branch (rule 3) **never** selects
    ``gbdt_quantile`` — that arm is structurally unreachable from the
    auto path because ``_should_use_prophet`` returns only prophet or
    linear_extrap. ``gbdt_quantile`` is only reachable via the explicit
    rule-1 dispatch.

    G10 / G14-G15 (Phase 3): ``context`` is forwarded to every
    forecaster (Prophet via build_anchored_timestamps + hour_baseline
    regressor; linear_extrap via trend blend + centroid intercept + p95
    clip; gbdt_quantile via lag/hour-baseline features + p95 cap).
    """
    if context is not None:
        logging.debug(
            "context forwarded: baseline=%d p95=%d trend_24h_slope=%.4f valid=%s",
            context.baseline_rps,
            context.peak_p95_rps,
            context.trend_24h_slope,
            context.hourly_profile_valid,
        )

    predicted: float
    model_used: ModelName

    if preferred_model == "gbdt_quantile":
        # Local import: gbdt_model pulls in LightGBM, which is a non-trivial
        # cold start. Skipping the import unless a gbdt request actually
        # arrives keeps startup cheap for Prophet/linear_extrap-only fleets.
        from forecast.gbdt_model import forecast_gbdt_quantile

        try:
            predicted = forecast_gbdt_quantile(
                rps_history, horizon_minutes, context=context
            )
            model_used = "gbdt_quantile"
        except Exception as exc:  # noqa: BLE001 - fall back on any failure
            logging.warning(
                "gbdt_quantile failed, falling back to linear_extrap: %s", exc
            )
            predicted = forecast_linear_extrap(
                rps_history, horizon_minutes, context=context
            )
            model_used = "linear_extrap"
    else:
        use_prophet = _should_use_prophet(
            rps_history=rps_history,
            prophet_min_points=prophet_min_points,
            preferred_model=preferred_model,
        )
        if use_prophet:
            try:
                predicted = forecast_prophet(
                    rps_history, horizon_minutes, context=context
                )
                model_used = "prophet"
            except Exception as exc:  # noqa: BLE001 - any Prophet failure is a fallback trigger
                logging.warning(
                    "prophet failed, falling back to linear_extrap: %s", exc
                )
                forecast_prophet_failures_total.inc()
                predicted = forecast_linear_extrap(
                    rps_history, horizon_minutes, context=context
                )
                model_used = "linear_extrap"
        else:
            predicted = forecast_linear_extrap(
                rps_history, horizon_minutes, context=context
            )
            model_used = "linear_extrap"

    # Single increment site for all forecasters: the label reflects the
    # *resolved* model (post-fallback), so a prophet→linear_extrap fallback
    # increments under linear_extrap, not prophet. The nightly E2E
    # (test/e2e/assertions-gbdt.sh) relies on this contract to verify
    # the gbdt_quantile path was actually exercised.
    forecast_dispatch_total.labels(model_used=model_used).inc()

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
