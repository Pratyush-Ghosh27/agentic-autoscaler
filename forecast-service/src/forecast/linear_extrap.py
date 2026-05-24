"""Linear extrapolation forecaster.

Per docs/design.md §5 forecast_linear_extrap pipeline:
1. Take the last min(10, len(rps_history)) points of rps_history.
2. Fit a least-squares line y = m*x + b (x = minute indices 0..n-1).
3. Extrapolate to x = n + (horizon_minutes - 1).
4. Return max(0, predicted_rps).
"""

from __future__ import annotations

import numpy as np


def forecast_linear_extrap(
    rps_history: list[float],
    horizon_minutes: int,
) -> float:
    """Predict RPS `horizon_minutes` ahead via least-squares linear fit.

    Uses up to the last 10 points of history to fit a line and extrapolates
    to the (horizon_minutes - 1)th point past the end of the series.
    """
    if not rps_history:
        raise ValueError("rps_history must not be empty")

    series = np.asarray(rps_history[-10:], dtype=float)
    n = len(series)

    if n == 1:
        return max(0.0, float(series[0]))

    x = np.arange(n, dtype=float)
    slope, intercept = np.polyfit(x, series, deg=1)

    target_x = n + horizon_minutes - 1
    predicted = slope * target_x + intercept

    return max(0.0, float(predicted))
