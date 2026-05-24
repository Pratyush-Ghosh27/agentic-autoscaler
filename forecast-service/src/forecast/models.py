"""Pydantic v2 request and response models for /recommend."""

from __future__ import annotations

from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator


class RecommendRequest(BaseModel):
    """Body of POST /recommend."""

    model_config = ConfigDict(extra="ignore")

    rps_history: Annotated[list[float], Field(min_length=1)]
    """Recent per-minute RPS values, oldest first."""

    workload_id: str | None = None
    """Free-form identifier; accepted but unused. Useful for tracing."""

    preferred_model: Literal["prophet", "linear_extrap", "auto"] | None = None
    """Override for model selection. None or 'auto' means defer to auto-select."""

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
    model_used: Literal["prophet", "linear_extrap"]
