"""Tests for the startup warm-up behaviour."""

from __future__ import annotations

import time

import pytest

from forecast.warmup import warmup_prophet


def test_warmup_completes_without_exception() -> None:
    """A single small Prophet fit should run cleanly."""
    warmup_prophet()


@pytest.mark.slow
def test_first_real_call_is_fast_after_warmup() -> None:
    """After warmup, the next prophet call must complete under 5 s
    (the controller's FORECAST_TIMEOUT_SECONDS default)."""
    warmup_prophet()
    from forecast.prophet_model import forecast_prophet

    series = [200.0 + 5.0 * i for i in range(70)]
    start = time.perf_counter()
    _ = forecast_prophet(series, horizon_minutes=10)
    elapsed = time.perf_counter() - start

    assert elapsed < 5.0, f"first post-warmup call took {elapsed:.2f}s"
