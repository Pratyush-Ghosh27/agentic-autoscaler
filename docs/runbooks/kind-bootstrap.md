# Kind Bootstrap Runbook

> Spin up a local 3-node kind cluster running the full Agentic Autoscaler
> stack (controller, forecast service, target apps for both reconcilers,
> Prometheus, Grafana, cert-manager) in roughly 10 minutes.

## Prerequisites

- Docker (running)
- Go 1.24+ (matches `go.mod`)
- Python 3.12+
- `make tools` (installs `controller-gen`, `kustomize`, `setup-envtest`,
  `golangci-lint`, `kubeconform` into `./bin/`)
- `kind` ≥ 0.24, `helm` ≥ 3.16, `kubectl` (any recent version)
- *(Optional)* `k6` for the load-generation runbooks
- *(Optional)* `ollama` for the explainability path; see
  [`ollama-setup.md`](ollama-setup.md)
- *(If upgrading from v1)* read [`../migrating-v1-to-v2.md`](../migrating-v1-to-v2.md) first; v2 env-var defaults differ from v1.

## Steps

### 1. Build images

```bash
make images
```

Produces `controller:<sha>`, `forecast-service:<sha>`, `target-app:<sha>`.

### 2. Create the cluster

```bash
make kind-up
```

The cluster config (`deploy/kind/cluster.yaml`) provisions one
control-plane and two worker nodes and exposes Grafana on host port 30030.

### 3. Load images into kind

```bash
make kind-load
```

### 4. Install cluster dependencies

```bash
make install-deps
```

This Helm-installs `cert-manager` and `kube-prometheus-stack` with the
CI-friendly value files in `deploy/helm/`. The Grafana sidecar will
auto-import the dashboard ConfigMap created in step 5.

### 5. Deploy the application stack

```bash
make deploy
```

Applies in dependency order:

1. `deploy/manifests/namespace.yaml` (`demo`, `agentic-system`)
2. `config/default` via Kustomize (controller manager + webhook + RBAC)
3. `forecast-service` Deployment + Service
4. `target-agentic` and `target-hpa` Deployments + Services
5. The HPA for `app-hpa`
6. `deploy/grafana` (dashboard ConfigMap via configMapGenerator)
7. The sample `AgenticAutoscaler` CR

### 6. Verify

```bash
kubectl get pods -A
kubectl get aas -A
kubectl get hpa -A
```

You should see the controller pod `Running` in `agentic-system`, the
forecast-service `Running`, and both `app-agentic` and `app-hpa` ready in
`demo`. The `aas` CR should show a `Phase` of `Ready` (or briefly
`Conflict` if the HPA list query happens to race the apply order — it
self-heals on the next reconcile).

### 7. Drive load

```bash
make k6-ramp
```

Or any of `k6-steady`, `k6-spiky`, `k6-bursty`. See
[`k6/README.md`](../../k6/README.md) for the full configuration knobs.

### 8. Observe

```bash
make port-forward-grafana
# http://localhost:3000  (admin / prom-operator)
```

The "Agentic Autoscaler" dashboard auto-loads from the configured
ConfigMap. See [`grafana-dashboard.md`](grafana-dashboard.md).

For raw Prometheus queries:

```bash
make port-forward-prometheus
# http://localhost:9090
```

## Teardown

```bash
make undeploy   # removes manifests but keeps the cluster
make kind-down  # nukes the kind cluster
```

## Troubleshooting

- **Pods pending** — check node resources: `kubectl describe nodes`. The
  3-node config is conservative; if your machine is tight on RAM you can
  drop one worker by editing `deploy/kind/cluster.yaml`.
- **cert-manager not ready** — `kubectl get pods -n cert-manager`; wait
  for all three pods (cainjector, controller, webhook) to become Ready
  before proceeding; the webhook in particular needs ~60 s.
- **Forecast service CrashLoopBackOff** — Prophet warm-up needs ~800 MB
  peak. Check the memory limit in
  `deploy/manifests/forecast-service.yaml`; bump it if your kind nodes
  are swapping.
- **Controller `forecast_unavailable` Events on first reconcile** —
  forecast service is still warming up; the next reconcile (60 s later)
  will succeed.
- **HPA-managed target stuck at `MinReplicas`** — the HPA's metric source
  (custom-metrics or Prometheus adapter) needs a few minutes to populate
  before it scales. This is not a bug; it's HPA behaviour.
