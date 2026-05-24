"""Shared pytest fixtures for the forecast-service test suite."""

from __future__ import annotations

import math

import numpy as np
import pytest


@pytest.fixture
def linear_series_30() -> list[float]:
    """A 30-point series with slope=2 rps/min, intercept=100."""
    return [100.0 + 2.0 * i for i in range(30)]


@pytest.fixture
def flat_series_30() -> list[float]:
    """A 30-point series of constant 200 rps."""
    return [200.0] * 30


@pytest.fixture
def periodic_series_120() -> list[float]:
    """A 120-point series with hourly periodicity (period=60 min)."""
    base = 200.0
    amplitude = 100.0
    return [
        base + amplitude * math.sin(2 * math.pi * i / 60.0)
        for i in range(120)
    ]


@pytest.fixture
def short_series_5() -> list[float]:
    """5 points — below PROPHET_MIN_POINTS for any sane threshold."""
    return [100.0, 110.0, 105.0, 108.0, 112.0]


@pytest.fixture
def reseeded_rng() -> np.random.Generator:
    """Deterministic RNG so noisy tests are reproducible."""
    return np.random.default_rng(seed=42)
