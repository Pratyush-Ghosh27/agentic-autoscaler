# Agentic Autoscaler

A Kubernetes operator that polls Prometheus for recent RPS history, asks
a Forecast Service to predict load `FORECAST_HORIZON_MINUTES` ahead,
classifies each workload's traffic pattern, and patches the target
Deployment's `/scale` subresource so capacity arrives **before** the load
does — not after.

A side-by-side HPA-managed comparison target is deployed alongside every
`AgenticAutoscaler`-managed Deployment so the project's claim ("we react
*before* the load arrives, the HPA reacts *after*") is verifiable on a
nightly schedule.

> Full design: [`docs/design.md`](docs/design.md). Implementation
> strategy: [`docs/superpowers/specs/2026-05-24-agentic-autoscaler-implementation-strategy.md`](docs/superpowers/specs/2026-05-24-agentic-autoscaler-implementation-strategy.md).

## Architecture at a glance

```
┌────────────────┐  range query   ┌──────────────┐
│  Prometheus    │◀───────────────│              │
└────────────────┘                │              │
                                  │  Controller  │  patches /scale
┌────────────────┐  /recommend    │  (Go)        │ ───────────────► Target Deployment
│  Forecast Svc  │◀───────────────│              │
│  (Python)      │                │              │
└────────────────┘                │              │
                                  │              │  ExplainRequest
┌────────────────┐  /v1/chat      │              │ ───────────────► ScaleExplained Event
│  Ollama        │◀───────────────│              │
└────────────────┘                └──────────────┘
```

Three workers run inside the controller process:

| Worker            | Cadence            | Purpose                                             |
| ----------------- | ------------------ | --------------------------------------------------- |
| Reconciler        | every 60 s         | Hot path: forecast → recommend → /scale → Event     |
| ClassifierWorker  | every 30 min       | Cold path: classify pattern, recommend params       |
| ExplainWorker     | drop-and-replace   | LLM-explanations of replica changes (best-effort)   |

Plus the in-cluster pieces: a target-app (Go) emitting
`http_requests_total` / `_request_duration_seconds_bucket`; the
Forecast Service (Python/FastAPI) running Prophet or linear extrapolation;
a 7-panel Grafana dashboard for the agentic-vs-HPA comparison.

## Quick start (local kind)

```sh
make tools             # one-shot: golangci-lint, kustomize, controller-gen, envtest, kubeconform
make images            # build all three container images
make kind-up           # 3-node kind cluster
make kind-load         # load images into kind
make install-deps      # Helm: cert-manager + kube-prometheus-stack
make deploy            # apply manifests + sample AgenticAutoscaler CR
make k6-ramp           # drive load and watch both targets scale
make port-forward-grafana   # http://localhost:3000  (admin / prom-operator)
```

Step-by-step walkthrough: [`docs/runbooks/kind-bootstrap.md`](docs/runbooks/kind-bootstrap.md).

## Required environment variables

The controller binary requires:

- `FORECAST_SERVICE_URL` — Forecast Service endpoint (e.g.
  `http://forecast-service.agentic-system.svc` — port 80, the in-cluster
  Service's default; not `:8000`, that's the container port)
- `PROMETHEUS_URL` — Prometheus endpoint

Everything else has documented defaults in
[`docs/design.md`](docs/design.md) §4. Notable knobs:

| Env var                            | Default                      | Purpose                                       |
| ---------------------------------- | ---------------------------- | --------------------------------------------- |
| `RECONCILE_INTERVAL_SECONDS`       | `60`                         | Hot path cadence                              |
| `FORECAST_HORIZON_MINUTES`         | `10`                         | How far ahead the forecast looks              |
| `CLASSIFIER_INTERVAL_MINUTES`      | `30`                         | Cold path cadence                             |
| `CLASSIFIER_MIN_POINTS`            | `70`                         | Required history before classification        |
| `OLLAMA_URL`                       | `http://localhost:11434`     | Ollama OpenAI-compat endpoint                 |
| `OLLAMA_MODEL`                     | `llama3.2`                   | LLM name (use `phi3` for CI/small machines)   |

## Repository layout

```
api/                         CRD types (kubebuilder)
cmd/controller/              Controller manager entrypoint
internal/
  classifier/                Cold-path: feature extraction, classify, params, worker
  config/                    Env-var → typed Config loader
  controller/                Reconciler + dependency interfaces
  decision/                  Pure scaling logic + state store
  explainer/                 ExplainWorker + prompt builder
  promql/                    PromQL string builders
  reasoning/                 Stable Event-reason / annotation tokens
  webhook/v1alpha1/          Validating admission webhook
internal/adapters/
  prometheus/                Prom HTTP client
  forecast/                  Forecast service client
  ollama/                    Ollama OpenAI-compat client
forecast-service/            Python FastAPI service (Prophet + linear_extrap)
target-app/                  Synthetic Go target with /work, /healthz, /metrics
hack/synthetic/              Deterministic test-data generator
testdata/                    Golden RPS fixtures (committed)
deploy/
  kind/                      kind cluster config
  helm/                      cert-manager + kube-prometheus-stack values
  manifests/                 Application Kubernetes manifests
  grafana/                   Dashboard ConfigMap (Kustomize)
k6/                          Load-generation scenarios (ramp/steady/spiky/bursty)
test/
  smoke/                     Pure-Go manifest validation + bash smoke
  e2e/                       k6-driven quantitative E2E + Prom assertions
.github/workflows/           PR CI + nightly E2E
docs/
  design.md                  System spec
  runbooks/                  kind-bootstrap, ollama-setup, nightly-e2e, grafana
```

## Development workflow

| Task                              | Command                                    |
| --------------------------------- | ------------------------------------------ |
| Run all linters                   | `make lint`                                |
| Run all unit + integration tests  | `make test test-envtest`                   |
| Re-generate CRD + DeepCopy        | `make manifests generate`                  |
| Re-generate testdata fixtures     | `make gen-testdata`                        |
| Build container images            | `make images`                              |
| Smoke test (5 min, kind)          | `make smoke`                               |
| Quantitative E2E (25 min, kind)   | `make e2e` *(or `make e2e-strict`)*        |

`make help` enumerates all targets. The full list is grouped under
*General / Environment / Code Generation / Lint / Test / Build / Cluster
Lifecycle / Scenarios / Smoke + E2E / Observability / Tooling*.

## CI

- **PR CI** (`.github/workflows/ci.yml`) — seven parallel jobs: lint,
  generate-check, test-go, test-python, test-envtest, build-images,
  smoke. Target time <10 min.
- **Nightly E2E** (`.github/workflows/nightly-e2e.yml`) — full kind
  cluster + helm + ollama + 25 min k6 ramp + Prometheus quantitative
  assertions. 60 min budget. Failure artifacts uploaded for inspection.

The PR CI's coverage gate is enforced on:

- `internal/...` Go coverage ≥ 80 %
- `forecast-service` Python coverage ≥ 90 %

(Per-package targets are higher; see `docs/superpowers/specs/`.)

## Troubleshooting

Start at the runbook closest to the problem:

- Cluster up but pods stuck — [`docs/runbooks/kind-bootstrap.md`](docs/runbooks/kind-bootstrap.md)
- ScaleExplained Events missing — [`docs/runbooks/ollama-setup.md`](docs/runbooks/ollama-setup.md)
- Nightly regression alarm — [`docs/runbooks/nightly-e2e.md`](docs/runbooks/nightly-e2e.md)
- Grafana panels empty / dashboard not loading — [`docs/runbooks/grafana-dashboard.md`](docs/runbooks/grafana-dashboard.md)

For controller-internal questions, every reasoning-token Event the
controller emits is enumerated in
[`internal/reasoning/tokens.go`](internal/reasoning/tokens.go) and tied
back to a section of `docs/design.md`.
