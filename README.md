# agentic-autoscaler

A Kubernetes operator that polls Prometheus for recent RPS history,
asks a Forecast Service to predict load `FORECAST_HORIZON_MINUTES`
ahead, classifies each workload's traffic pattern, and patches the
target Deployment's `/scale` subresource so capacity arrives before
load does.

See [`docs/design.md`](docs/design.md) for the full system specification
and [`docs/superpowers/specs/2026-05-24-agentic-autoscaler-implementation-strategy.md`](docs/superpowers/specs/2026-05-24-agentic-autoscaler-implementation-strategy.md)
for the implementation strategy.

## Status

Plan #1 (skeleton + CRD + manager) — complete. The CRD types and the
typed env-var config loader compile and pass tests. The manager binary
loads its config at startup and registers the v1alpha1 scheme. The
reconciler, workers, adapters, and admission webhook arrive in later
plans.

## Build

```bash
go build ./...
go test ./...
```

## Required environment

- `FORECAST_SERVICE_URL` — Forecast Service endpoint
- `PROMETHEUS_URL` — Prometheus endpoint

All other env vars have defaults documented in `docs/design.md` §4.
