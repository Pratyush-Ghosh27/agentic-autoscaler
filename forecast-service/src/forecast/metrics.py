"""Prometheus metrics exported by the forecast service."""

from __future__ import annotations

from prometheus_client import Counter

forecast_prophet_failures_total = Counter(
    "forecast_prophet_failures_total",
    "Number of times Prophet raised during /recommend; dispatcher fell back to linear_extrap.",
)
