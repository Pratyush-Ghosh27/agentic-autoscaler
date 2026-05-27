"""Prophet-based forecaster.

Per docs/design_v2.md §5 forecast_prophet pipeline (F3a, F17):

1. Build a DataFrame:
     ds = synthetic 1-minute timestamps ending at
          (context.current_hour_utc, context.current_minute_utc) when
          context is provided, else at the service's local UTC clock
          (legacy cold-start path).
     y  = rps_history values.
2. If ``context`` is provided AND ``context.hourly_profile_valid`` AND
   the ``PROPHET_USE_HOURLY_REGRESSOR`` env var is truthy (default
   ``"true"``), attach the 24-bin profile as an external regressor
   named ``hour_baseline``.
3. Fit Prophet with daily/weekly seasonality disabled,
   ``changepoint_prior_scale=0.5``.
4. Build a future DataFrame extending ``horizon_minutes`` past the
   last ``ds``.
5. ``predicted_rps = model.predict(future).iloc[-1].yhat``.
6. Return ``max(0.0, predicted_rps)``.

Prophet rejects timezone-aware timestamps, so we build naive UTC
datetimes throughout.
"""

from __future__ import annotations

import os
from datetime import UTC, datetime, timedelta
from typing import TYPE_CHECKING

import pandas as pd
from prophet import Prophet

if TYPE_CHECKING:
    from forecast.models import ContextPayload


def build_anchored_timestamps(
    n: int,
    current_hour_utc: int | None = None,
    current_minute_utc: int | None = None,
) -> list[pd.Timestamp]:
    """Return ``n`` 1-minute-spaced naive UTC ``pandas.Timestamp``s whose
    last entry has ``(hour, minute) == (current_hour_utc, current_minute_utc)``.

    If either ``current_hour_utc`` or ``current_minute_utc`` is None, fall
    back to the service's local UTC wall clock (legacy cold-start
    behaviour). This keeps callers that have no ContextPayload — for
    example, the very first reconcile after install — fully working.

    Prophet only cares about contiguous 1-minute spacing, not absolute
    calendar dates, so we anchor today's date and walk back to the
    requested (h, m).
    """
    if current_hour_utc is None or current_minute_utc is None:
        end = datetime.now(tz=UTC).replace(second=0, microsecond=0, tzinfo=None)
    else:
        now = datetime.now(tz=UTC).replace(second=0, microsecond=0, tzinfo=None)
        end = now.replace(hour=current_hour_utc, minute=current_minute_utc)
    return [pd.Timestamp(end - timedelta(minutes=(n - 1 - i))) for i in range(n)]


def forecast_prophet(
    rps_history: list[float],
    horizon_minutes: int,
    context: ContextPayload | None = None,
) -> float:
    """Predict RPS ``horizon_minutes`` ahead using Prophet.

    Raises any exception Prophet raises during fit; the caller
    (:func:`forecast.dispatch.recommend`) is responsible for catching
    it and falling back to linear_extrap.
    """
    if not rps_history:
        raise ValueError("rps_history must not be empty")
    if horizon_minutes < 0:
        raise ValueError("horizon_minutes must be >= 0")

    n = len(rps_history)
    timestamps = build_anchored_timestamps(
        n=n,
        current_hour_utc=context.current_hour_utc if context is not None else None,
        current_minute_utc=(context.current_minute_utc if context is not None else None),
    )

    df = pd.DataFrame({"ds": timestamps, "y": rps_history})

    use_hour_regressor = (
        context is not None
        and context.hourly_profile_valid
        and os.environ.get("PROPHET_USE_HOURLY_REGRESSOR", "true").lower() == "true"
    )
    if use_hour_regressor:
        assert context is not None  # narrow for type-checkers
        df["hour_baseline"] = [
            float(context.hourly_profile[t.hour]) for t in df["ds"]
        ]

    model = Prophet(
        daily_seasonality=False,
        weekly_seasonality=False,
        changepoint_prior_scale=0.5,
    )
    if use_hour_regressor:
        model.add_regressor("hour_baseline")
    model.fit(df)

    future = model.make_future_dataframe(
        periods=max(1, horizon_minutes),
        freq="min",
        include_history=False,
    )
    if use_hour_regressor:
        assert context is not None
        future["hour_baseline"] = [
            float(context.hourly_profile[t.hour]) for t in future["ds"]
        ]

    forecast = model.predict(future)
    if horizon_minutes == 0:
        predicted = float(forecast["yhat"].iloc[0])
    else:
        predicted = float(forecast["yhat"].iloc[-1])

    return max(0.0, predicted)
