"""Tests for the Prometheus metrics exported by the forecast service."""

from __future__ import annotations

import pytest

from forecast.metrics import (
    forecast_dispatch_total,
    forecast_prophet_failures_total,
)


def test_forecast_dispatch_total_exists_with_model_used_label() -> None:
    """The counter exists and has exactly one label, named ``model_used``.

    A nightly E2E asserts on this metric to lock in the gbdt_quantile path
    (Plan 18). Renaming the metric or its label is a breaking change for
    test/e2e/assertions-gbdt.sh.
    """
    assert forecast_dispatch_total._name == "forecast_dispatch"
    assert forecast_dispatch_total._labelnames == ("model_used",)
    forecast_dispatch_total.labels(model_used="prophet").inc(0)
    samples = list(forecast_dispatch_total.collect())
    sample_names = {s.name for fam in samples for s in fam.samples}
    assert "forecast_dispatch_total" in sample_names, (
        "scraped sample name must be forecast_dispatch_total (Prometheus convention "
        "appends _total at scrape time); got {sample_names}"
    )


@pytest.mark.parametrize("name", ["prophet", "linear_extrap", "gbdt_quantile"])
def test_forecast_dispatch_total_accepts_each_model_used_value(name: str) -> None:
    """The three v2 forecaster names are valid label values and increment cleanly."""
    before = forecast_dispatch_total.labels(model_used=name)._value.get()
    forecast_dispatch_total.labels(model_used=name).inc()
    after = forecast_dispatch_total.labels(model_used=name)._value.get()
    assert after == before + 1


def test_forecast_prophet_failures_total_is_unchanged() -> None:
    """The pre-existing counter is still exported (regression guard)."""
    assert forecast_prophet_failures_total._name == "forecast_prophet_failures"
