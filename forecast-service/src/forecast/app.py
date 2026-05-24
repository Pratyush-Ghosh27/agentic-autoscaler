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

# These two values are read from env at startup. The controller and the
# Forecast Service must keep them in sync per design §4 / §5.
FORECAST_HORIZON_MINUTES = int(os.environ.get("FORECAST_HORIZON_MINUTES", "10"))
PROPHET_MIN_POINTS = int(os.environ.get("PROPHET_MIN_POINTS", "60"))


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
