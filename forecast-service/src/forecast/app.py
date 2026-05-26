"""FastAPI service for the forecast endpoint."""

from __future__ import annotations

import logging
import os
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI
from fastapi.responses import Response
from prometheus_client import CONTENT_TYPE_LATEST, generate_latest

from forecast.dispatch import recommend
from forecast.models import RecommendRequest, RecommendResponse

# Service-side env vars, read once at module import.
#
# Per F36 the forecast horizon is a Forecast Service property (not a
# controller property): the service publishes it via every /recommend
# response and the controller stamps that value into ExplainRequest.
#
# PROPHET_MIN_POINTS default lowered from 60 to 30 (F2a-revisited):
# Prophet now learns from shorter histories; its own changepoint and
# seasonality gates handle low-confidence cases internally.
#
# The remaining vars are forecaster-tuning knobs (F24, G21) plus the
# context-validation threshold (G11). They are accessed by attribute
# from the dispatch layer; the integration test pins their defaults.
FORECAST_HORIZON_MINUTES = int(os.environ.get("FORECAST_HORIZON_MINUTES", "10"))
PROPHET_MIN_POINTS = int(os.environ.get("PROPHET_MIN_POINTS", "30"))
LINEAR_EXTRAP_RECENT_WEIGHT = float(
    os.environ.get("LINEAR_EXTRAP_RECENT_WEIGHT", "0.7")
)
LINEAR_EXTRAP_WINDOW_MINUTES = int(
    os.environ.get("LINEAR_EXTRAP_WINDOW_MINUTES", "10")
)
GBDT_QUANTILE = float(os.environ.get("GBDT_QUANTILE", "0.90"))
GBDT_MIN_POINTS = int(os.environ.get("GBDT_MIN_POINTS", "30"))
PROPHET_USE_HOURLY_REGRESSOR = (
    os.environ.get("PROPHET_USE_HOURLY_REGRESSOR", "true").lower() == "true"
)
HOURLY_PROFILE_MIN_HOURS = int(os.environ.get("HOURLY_PROFILE_MIN_HOURS", "12"))


@asynccontextmanager
async def _lifespan(_app: FastAPI) -> AsyncIterator[None]:
    if os.environ.get("FORECAST_SKIP_WARMUP") != "1":
        # Imported lazily so unit-test collection doesn't pay the import cost.
        from forecast.warmup import warmup_prophet

        warmup_prophet()
    else:
        logging.info("FORECAST_SKIP_WARMUP=1; skipping prophet warm-up")
    yield


app = FastAPI(title="agentic-forecast", version="0.1.0", lifespan=_lifespan)


@app.get("/healthz")
async def healthz() -> dict[str, str]:
    return {"status": "ok"}


@app.get("/metrics")
async def metrics() -> Response:
    """Prometheus scrape endpoint."""
    return Response(content=generate_latest(), media_type=CONTENT_TYPE_LATEST)


@app.post("/recommend", response_model=RecommendResponse)
async def post_recommend(req: RecommendRequest) -> RecommendResponse:
    result = recommend(
        rps_history=req.rps_history,
        horizon_minutes=FORECAST_HORIZON_MINUTES,
        prophet_min_points=PROPHET_MIN_POINTS,
        preferred_model=req.preferred_model,
    )
    return RecommendResponse(**result)
