"""Prophet-based forecaster.

Per docs/design.md §5 forecast_prophet pipeline:
1. Build a DataFrame: ds = synthetic 1-minute timestamps ending now;
                       y  = rps_history values.
2. Fit Prophet with daily/weekly seasonality disabled,
   changepoint_prior_scale=0.5.
3. Build a future DataFrame extending horizon_minutes past the last ds.
4. predicted_rps = model.predict(future).iloc[-1].yhat.
5. Return max(0.0, predicted_rps).

Prophet rejects timezone-aware timestamps, so we build naive UTC
datetimes (datetime.now(UTC) followed by .replace(tzinfo=None)).
"""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pandas as pd
from prophet import Prophet


def forecast_prophet(
    rps_history: list[float],
    horizon_minutes: int,
) -> float:
    """Predict RPS `horizon_minutes` ahead using Prophet.

    Raises any exception Prophet raises during fit; the caller
    (dispatch.recommend) is responsible for catching and falling back.
    """
    if not rps_history:
        raise ValueError("rps_history must not be empty")
    if horizon_minutes < 0:
        raise ValueError("horizon_minutes must be >= 0")

    n = len(rps_history)
    end = datetime.now(tz=UTC).replace(second=0, microsecond=0, tzinfo=None)
    timestamps = [end - timedelta(minutes=(n - 1 - i)) for i in range(n)]

    df = pd.DataFrame({"ds": timestamps, "y": rps_history})

    model = Prophet(
        daily_seasonality=False,
        weekly_seasonality=False,
        changepoint_prior_scale=0.5,
    )
    model.fit(df)

    future = model.make_future_dataframe(
        periods=max(1, horizon_minutes),
        freq="min",
        include_history=False,
    )
    forecast = model.predict(future)
    if horizon_minutes == 0:
        # Prophet's make_future_dataframe requires periods >= 1; we fitted
        # with 1 future minute and slice back to the first row, which is
        # the value at end+1min. For horizon_minutes==0 the natural answer
        # is the last *observed* point, but design.md keeps the same
        # extrapolation pipeline for parity with linear_extrap; clamp to
        # non-negative and return.
        predicted = float(forecast["yhat"].iloc[0])
    else:
        predicted = float(forecast["yhat"].iloc[-1])

    return max(0.0, predicted)
