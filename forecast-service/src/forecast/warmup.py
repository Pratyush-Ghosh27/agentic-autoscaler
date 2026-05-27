"""Startup warm-up.

Per docs/design_v2.md §5: the service performs one dummy Prophet fit on a
small synthetic series during startup so the first real /recommend call
doesn't pay the Stan compilation cost.
"""

from __future__ import annotations

import logging

from forecast.prophet_model import forecast_prophet


def warmup_prophet() -> None:
    """Fit Prophet once on a 90-point synthetic series. Best-effort: any
    exception is logged and swallowed because warmup is a perf
    optimisation, not a hard prerequisite for serving traffic."""
    series = [200.0 + 0.5 * i for i in range(90)]
    try:
        _ = forecast_prophet(series, horizon_minutes=10)
        logging.info("prophet warmup complete")
    except Exception as exc:  # noqa: BLE001 - warmup is best-effort
        logging.warning("prophet warmup failed: %s", exc)
