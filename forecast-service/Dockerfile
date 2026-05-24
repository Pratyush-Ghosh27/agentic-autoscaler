# syntax=docker/dockerfile:1.7

# ---- builder: install all deps including build-time prophet/cmdstanpy ----
FROM python:3.12-slim AS builder

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build
COPY pyproject.toml ./
COPY src ./src

RUN pip install --upgrade pip && \
    pip install build && \
    python -m build --wheel --outdir /dist .

# ---- runtime: slim image with only the wheel + runtime deps ----
FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PORT=8000

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgomp1 \
    && rm -rf /var/lib/apt/lists/* && \
    useradd --create-home --shell /bin/bash forecast

COPY --from=builder /dist/*.whl /tmp/

# Pre-warm prophet's Stan model into the image so first request is fast.
RUN pip install --upgrade pip && \
    pip install /tmp/*.whl uvicorn[standard] && \
    rm -f /tmp/*.whl && \
    python -c "from prophet import Prophet; p = Prophet(); print('prophet ok')" || true

USER forecast
EXPOSE 8000
CMD ["uvicorn", "forecast.app:app", "--host", "0.0.0.0", "--port", "8000"]
