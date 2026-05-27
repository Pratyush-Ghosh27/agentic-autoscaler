"""Prometheus metrics exported by the forecast service."""

from __future__ import annotations

from prometheus_client import Counter

forecast_prophet_failures_total = Counter(
    "forecast_prophet_failures_total",
    "Number of times Prophet raised during /recommend; dispatcher fell back to linear_extrap.",
)

forecast_dispatch_total = Counter(
    "forecast_dispatch_total",
    "Cumulative count of successful /recommend dispatches, by resolved model_used.",
    labelnames=["model_used"],
)
