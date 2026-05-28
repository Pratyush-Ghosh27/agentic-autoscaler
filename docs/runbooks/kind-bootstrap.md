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

1. `deploy/manifests/namespace.yaml` (creates `demo` and `agentic-system`)
2. `config/default` via Kustomize (controller manager + webhook + RBAC, into the kustomize-default namespace `agentic-autoscaler-system` — created implicitly by the overlay)
3. `forecast-service` Deployment + Service (in `agentic-system`)
4. `target-agentic` and `target-hpa` Deployments + Services (in `demo`)
5. The HPA for `app-hpa`
6. `deploy/grafana` (dashboard ConfigMap via configMapGenerator)
7. The sample `AgenticAutoscaler` CR

> **Three namespaces, not two.** The controller manager and webhook live in
> `agentic-autoscaler-system` (the kubebuilder default, set in
> `config/default/kustomization.yaml`). The forecast-service lives in
> `agentic-system`. Application workloads live in `demo`. This is a
> historical kubebuilder-scaffold split that the project never collapsed —
> `kubectl logs -n agentic-system <controller-pod>` returns "No resources
> found" because the controller isn't there.

### 6. Verify

```bash
kubectl get pods -A
kubectl get aas -A
kubectl get hpa -A
```

You should see the controller pod `Running` in `agentic-autoscaler-system`,
the forecast-service `Running` in `agentic-system`, and both `app-agentic`
and `app-hpa` ready in `demo`. The `aas` CR should show a `Phase` of
`Ready` (or briefly `Conflict` if the HPA list query happens to race the
apply order — it self-heals on the next reconcile). `Phase` stays blank
for the first ~10 minutes after deploy while the hot path accumulates
`HOT_PATH_MIN_POINTS=10` of Prometheus history; that's by design.

### 7. Drive load

```bash
make k6-incluster-ramp
```

Or any of `k6-incluster-steady`, `k6-incluster-spiky`, `k6-incluster-bursty`.
These run k6 as a Job inside the cluster so traffic hits the Services'
ClusterIP and gets real kube-proxy load-balancing across all replicas.

> **Don't use `make k6-ramp` (the host-mode target) for autoscaler
> evaluation.** It posts to `localhost:8080`/`8081`, requires manual
> `kubectl port-forward svc/...`, and inherits the single-pod-pinning
> problem from [kubernetes/kubernetes#15180](https://github.com/kubernetes/kubernetes/issues/15180)
> — only one replica per side ever receives traffic. The host-mode
> targets exist for debugging a known single pod, not for comparing
> autoscaler behaviour. See [`k6/README.md`](../../k6/README.md) for the
> full configuration knobs and the in-cluster-vs-host trade-off.

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
- **k6 pod or other workloads fail with `failed to create fsnotify watcher: too many open files`** —
  on RHEL/CentOS hosts `fs.inotify.max_user_instances` defaults to **128**,
  which is exhausted by a 3-node kind cluster running cert-manager +
  kube-prometheus-stack + the application stack + a k6 Job (each fsnotify
  watcher consumes one inotify instance, and the symptom appears on the
  next pod that calls `inotify_init1()` after the budget is gone). Raise
  the limit:
  ```bash
  sudo sysctl -w fs.inotify.max_user_instances=8192
  sudo sysctl -w fs.inotify.max_user_watches=524288
  # persist across reboots:
  sudo tee /etc/sysctl.d/99-kind.conf >/dev/null <<'EOF'
  fs.inotify.max_user_instances = 8192
  fs.inotify.max_user_watches   = 524288
  EOF
  sudo sysctl --system
  ```
  New pods pick up the higher limit immediately; existing pods that
  already errored may need a `kubectl rollout restart`. Documented as the
  first entry of [kind's known-issues page](https://kind.sigs.k8s.io/docs/user/known-issues/#pod-errors-due-to-too-many-open-files).
- **`AgenticAutoscaler` CR's `Phase` column stays blank for several
  minutes after deploy** — the hot-path reconciler short-circuits with
  `MetricsUnavailable` Events until it accumulates `HOT_PATH_MIN_POINTS=10`
  Prometheus samples (~10 min at the 60s scrape interval). The
  `Pattern` column stays blank for ~72 min while the cold-path classifier
  builds up `CLASSIFIER_MIN_POINTS=72` samples. Both are by design — watch
  `kubectl get events -n demo --field-selector involvedObject.name=app-agentic`
  to see progress monotonically advance through the sample count.
