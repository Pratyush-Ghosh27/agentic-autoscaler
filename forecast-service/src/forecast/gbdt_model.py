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
   horizon_minutes)`` so the row's ``hour_baseline`` lines up with the
   target's wall clock.
4. ``predicted_rps = clamp(model.predict([X_pred])[0], 0,
   context.peak_p95_rps * 3)`` — the safety cap (T11) is the only
   thing standing between a runaway quantile fit and a recommendation
   that would page-out the cluster.

Phase 3 contract: feature engineering MUST use only context fields
and rps_history; never the service-local clock (F21).
"""

from __future__ import annotations

import os
from typing import TYPE_CHECKING

import numpy as np

if TYPE_CHECKING:
    from forecast.models import ContextPayload


GBDT_MIN_POINTS_DEFAULT = 30
GBDT_QUANTILE_DEFAULT = 0.90
GBDT_LAG_DEPTH = 3


def _min_points() -> int:
    """Minimum training rows. Operator-tunable via ``GBDT_MIN_POINTS``.

    Malformed / non-positive values fall back to the default so an
    operator typo cannot take the hot path offline.
    """
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


def _quantile() -> float:
    """LightGBM quantile alpha. Operator-tunable via ``GBDT_QUANTILE``.

    Malformed values or ones outside ``(0, 1)`` fall back to the
    default (0.90)."""
    raw = os.environ.get("GBDT_QUANTILE")
    if raw is None:
        return GBDT_QUANTILE_DEFAULT
    try:
        value = float(raw)
    except ValueError:
        return GBDT_QUANTILE_DEFAULT
    if not (0.0 < value < 1.0):
        return GBDT_QUANTILE_DEFAULT
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

    # Anchor: timestamp of the *last* observed sample. F21 — this MUST
    # come from context, never from the service-local clock. With no
    # context (cold-start path), we degrade to a no-op clock: hour=0,
    # minute=0, profile=zeros — feature engineering then carries no
    # hour-of-day information and the model effectively becomes a pure
    # lag predictor. The dispatcher (T12) only routes here when context
    # is present, so this branch is exercised by tests, not production.
    if context is None:
        anchor_hour = 0
        anchor_minute = 0
        hourly_profile: list[int] = [0] * 24
    else:
        anchor_hour = context.current_hour_utc
        anchor_minute = context.current_minute_utc
        hourly_profile = list(context.hourly_profile)

    n = len(rps_history)
    lag = GBDT_LAG_DEPTH

    rows_x: list[list[float]] = []
    rows_y: list[float] = []
    # i is the index of the row's *first* lag sample.
    # cur_idx = i + lag - 1 is the row's most recent observed sample.
    # target_idx = cur_idx + horizon_minutes is the supervised target.
    # The length-gate above guarantees at least one legal i.
    for i in range(0, n - lag - horizon_minutes + 1):
        cur_idx = i + lag - 1
        target_idx = cur_idx + horizon_minutes
        if target_idx >= n:
            break

        # Walk back from the anchor to derive this row's hour-of-day.
        minutes_ago = (n - 1) - cur_idx
        total_minute = (anchor_hour * 60 + anchor_minute - minutes_ago) % (24 * 60)
        row_hour = total_minute // 60
        row_minute_in_hour = total_minute % 60

        rows_x.append(
            [
                float(rps_history[i]),
                float(rps_history[i + 1]),
                float(rps_history[i + 2]),
                float(hourly_profile[row_hour]),
                float(row_minute_in_hour),
            ]
        )
        rows_y.append(float(rps_history[target_idx]))

    if not rows_x:
        # Defensive — the length gate above guards this, but make the
        # failure mode explicit so a future edit that drifts the math
        # gets a clear error message instead of a LightGBM exception.
        raise ValueError("gbdt_quantile produced zero training rows")

    # Local import: keeps process startup cheap when no /recommend
    # request ever needs gbdt_quantile (most workloads will not).
    import lightgbm as lgb

    model = lgb.LGBMRegressor(
        objective="quantile",
        alpha=_quantile(),
        n_estimators=80,
        learning_rate=0.1,
        num_leaves=15,
        min_data_in_leaf=2,
        verbose=-1,
    )
    model.fit(np.asarray(rows_x), np.asarray(rows_y))

    # Prediction row: the most recent 3 samples + the hour/minute the
    # forecast is *targeting* (anchor + horizon).
    pred_total_minute = (anchor_hour * 60 + anchor_minute + horizon_minutes) % (
        24 * 60
    )
    pred_hour = pred_total_minute // 60
    pred_minute_in_hour = pred_total_minute % 60

    pred_row = np.asarray(
        [
            [
                float(rps_history[-3]),
                float(rps_history[-2]),
                float(rps_history[-1]),
                float(hourly_profile[pred_hour]),
                float(pred_minute_in_hour),
            ]
        ]
    )
    predicted = float(model.predict(pred_row)[0])

    return max(0.0, predicted)  # T11 lands the upper safety cap.
