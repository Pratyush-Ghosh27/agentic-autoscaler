"""GBDT quantile forecaster (G12, F21).

Per docs/design_v2.md §5 ``forecast_gbdt_quantile`` pipeline:

1. Build training rows from ``rps_history`` shifted by
   ``horizon_minutes``:
     - ``y_train[i] = rps_history[cur_idx + horizon_minutes]``
     - ``X_train[i] = [lag_1, lag_2, lag_3, hour_baseline, minute_in_hour]``
   where ``cur_idx = i + lag - 1`` is the position of the row's most
   recent observed sample and ``lag = 3`` is the lag depth.
2. Train a LightGBM quantile regressor at ``GBDT_QUANTILE`` (default
   0.90) — we want the upper tail to absorb spikes.
3. Build the prediction row from the *last* observed sample, anchored
   to ``(context.current_hour_utc, context.current_minute_utc +
   horizon_minutes)`` so the row's hour_baseline lines up with the
   target's wall clock.
4. ``predicted_rps = clamp(model.predict([X_pred])[0], 0,
   context.peak_p95_rps * 3)`` — the safety cap (T11) is the only
   thing standing between a runaway quantile fit and a recommendation
   that would page-out the cluster.

T9 (this commit) is the skeleton: defensive validation + length
gate + ``NotImplementedError`` placeholder so the import works and
the smoke test fails at the documented stop sign. T10 fills in the
feature engineering and training; T11 lands the safety cap.

Phase 3 contract: feature engineering MUST use only context fields
and rps_history; never the service-local clock (F21).
"""

from __future__ import annotations

import os
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from forecast.models import ContextPayload


GBDT_MIN_POINTS_DEFAULT = 30


def _min_points() -> int:
    """Minimum training rows. Operator-tunable via GBDT_MIN_POINTS."""
    raw = os.environ.get("GBDT_MIN_POINTS")
    if raw is None:
        return GBDT_MIN_POINTS_DEFAULT
    try:
        value = int(raw)
    except ValueError:
        return GBDT_MIN_POINTS_DEFAULT
    if value <= 0:
        return GBDT_MIN_POINTS_DEFAULT
    return value


def forecast_gbdt_quantile(
    rps_history: list[float],
    horizon_minutes: int,
    context: ContextPayload | None = None,
) -> float:
    """Predict RPS ``horizon_minutes`` ahead using a LightGBM quantile regressor.

    Raises:
        ValueError: if ``rps_history`` is empty, ``horizon_minutes`` is
            negative, or the history is shorter than
            ``GBDT_MIN_POINTS + horizon_minutes`` (the lag/horizon
            shift would otherwise leave zero training rows).
    """
    if not rps_history:
        raise ValueError("rps_history must not be empty")
    if horizon_minutes < 0:
        raise ValueError("horizon_minutes must be >= 0")

    min_pts = _min_points()
    if len(rps_history) < min_pts + horizon_minutes:
        raise ValueError(
            f"gbdt_quantile requires len(rps_history) >= GBDT_MIN_POINTS "
            f"+ horizon_minutes = {min_pts + horizon_minutes}, "
            f"got {len(rps_history)}"
        )

    raise NotImplementedError(
        "T10 implements forecast_gbdt_quantile's training + predict body"
    )
