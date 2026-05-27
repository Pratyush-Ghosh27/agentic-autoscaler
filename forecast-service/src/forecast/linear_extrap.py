"""Linear extrapolation forecaster.

Per docs/design_v2.md §5 forecast_linear_extrap pipeline:

1. Take the last ``min(LINEAR_EXTRAP_WINDOW_MINUTES, len(rps_history))``
   points of ``rps_history`` (default 10; T5 / G15 makes this env-tunable).
2. Fit a least-squares line ``y = m*x + b`` with ``x`` = minute indices
   ``0..n-1``.
3. Extrapolate to ``x = n + (horizon_minutes - 1)``.
4. Return ``max(0, predicted_rps)``.

Phase 3 will additionally:
  - blend ``m`` with ``context.trend_24h_slope`` via
    ``LINEAR_EXTRAP_RECENT_WEIGHT`` (T6, F16),
  - recompute ``b`` from the window centroid after the blend (T7, F31),
  - clip the prediction at ``context.peak_p95_rps * 1.5`` (T8, G15).
"""

from __future__ import annotations

import logging
import os

import numpy as np

_DEFAULT_WINDOW_MINUTES = 10


def _window_minutes() -> int:
    """Return the linear-fit window length in minutes (T5 / G15).

    Defaults to ``_DEFAULT_WINDOW_MINUTES``. A non-integer or
    non-positive value in ``LINEAR_EXTRAP_WINDOW_MINUTES`` is logged
    and treated as "use the default" so a typo in the operator's
    ConfigMap cannot take the hot path offline.
    """
    raw = os.environ.get("LINEAR_EXTRAP_WINDOW_MINUTES")
    if raw is None:
        return _DEFAULT_WINDOW_MINUTES
    try:
        value = int(raw)
    except ValueError:
        logging.warning(
            "LINEAR_EXTRAP_WINDOW_MINUTES=%r is not an integer; "
            "falling back to default=%d",
            raw,
            _DEFAULT_WINDOW_MINUTES,
        )
        return _DEFAULT_WINDOW_MINUTES
    if value <= 0:
        logging.warning(
            "LINEAR_EXTRAP_WINDOW_MINUTES=%d is non-positive; "
            "falling back to default=%d",
            value,
            _DEFAULT_WINDOW_MINUTES,
        )
        return _DEFAULT_WINDOW_MINUTES
    return value


def forecast_linear_extrap(
    rps_history: list[float],
    horizon_minutes: int,
) -> float:
    """Predict RPS ``horizon_minutes`` ahead via least-squares linear fit.

    Uses up to the last ``LINEAR_EXTRAP_WINDOW_MINUTES`` points of
    history (default 10) to fit a line and extrapolates to the
    ``(horizon_minutes - 1)``th point past the end of the series.
    """
    if not rps_history:
        raise ValueError("rps_history must not be empty")

    window = _window_minutes()
    series = np.asarray(rps_history[-window:], dtype=float)
    n = len(series)

    if n == 1:
        return max(0.0, float(series[0]))

    x = np.arange(n, dtype=float)
    slope, intercept = np.polyfit(x, series, deg=1)

    target_x = n + horizon_minutes - 1
    predicted = slope * target_x + intercept

    return max(0.0, float(predicted))
