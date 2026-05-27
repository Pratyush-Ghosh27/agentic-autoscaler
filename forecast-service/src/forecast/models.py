"""Pydantic v2 request and response models for /recommend."""

from __future__ import annotations

from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator


class ContextPayload(BaseModel):
    """Cold-path-computed scalar features and a 24-bin hourly profile,
    plus the current-time fields the controller stamps at request build
    time. The controller forwards this verbatim from
    ``status.classifiedParams.context`` (with current_hour_utc /
    current_minute_utc added). Forecasters consume these fields to bias
    predictions when ``hourly_profile_valid`` is True; otherwise they
    must ignore ``hourly_profile``.

    See docs/design_v2.md §6.1 step 6.5 (computation) and §6.3
    (forwarding contract). G10 wires this end-to-end.
    """

    model_config = ConfigDict(extra="ignore")

    baseline_rps: int
    """Median RPS over the cold-path history window (typically 7 days
    at 5-min cadence)."""

    peak_p95_rps: int
    """95th-percentile RPS over the cold-path history window."""

    trend_24h_slope: float
    """24-hour rolling trend slope, units rps/min. Positive means
    rising load (F18)."""

    hourly_profile: Annotated[list[int], Field(min_length=24, max_length=24)]
    """24-bin median-of-RPS-per-UTC-hour profile. Index 0 = 00:00 UTC,
    index 23 = 23:00 UTC."""

    hourly_profile_valid: bool
    """True iff every UTC-hour bin had at least HOURLY_PROFILE_MIN_HOURS
    samples; forecasters must ignore hourly_profile when False."""

    current_hour_utc: Annotated[int, Field(ge=0, le=23)]
    """Wall-clock UTC hour at request build time, 0..23."""

    current_minute_utc: Annotated[int, Field(ge=0, le=59)]
    """Wall-clock UTC minute at request build time, 0..59."""


class RecommendRequest(BaseModel):
    """Body of POST /recommend."""

    model_config = ConfigDict(extra="ignore")

    rps_history: Annotated[list[float], Field(min_length=1)]
    """Recent per-minute RPS values, oldest first."""

    workload_id: str | None = None
    """Free-form identifier; accepted but unused. Useful for tracing."""

    preferred_model: (
        Literal["prophet", "linear_extrap", "gbdt_quantile", "auto"] | None
    ) = None
    """Override for model selection. None or 'auto' means defer to auto-select.

    Note: G12 (Phase 3) — "gbdt_quantile" is an opt-in spike-aware
    forecaster. F22 (mirrored in the Go classifier in T13) guarantees
    that classifier auto-mode never selects it, so this value can only
    arrive here when the user explicitly pinned it via the CRD spec."""

    context: ContextPayload | None = None
    """Cold-path-computed context. None means "no context" — either the
    controller has not run a classification cycle yet, or the user has
    engaged the autoscaling.agentic.io/skip-context annotation."""

    @field_validator("rps_history")
    @classmethod
    def _all_non_negative(cls, v: list[float]) -> list[float]:
        if any(x < 0 for x in v):
            raise ValueError("rps_history values must be non-negative")
        return v


class RecommendResponse(BaseModel):
    """Body of POST /recommend response."""

    predicted_rps: float
    horizon_minutes: int
    model_used: Literal["prophet", "linear_extrap", "gbdt_quantile"]
