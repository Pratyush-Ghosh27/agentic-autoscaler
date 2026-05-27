# `deploy/` — Cluster manifests and observability

Everything required to spin up a local kind cluster running the
AgenticAutoscaler controller alongside its kube-prometheus-stack and a
side-by-side HPA comparison workload.

```text
deploy/
├── kind/cluster.yaml                # 3-node kind cluster
├── helm/
│   ├── prometheus-values.yaml       # kube-prometheus-stack values
│   └── certmanager-values.yaml      # cert-manager values (CRDs included)
├── manifests/
│   ├── namespace.yaml               # demo + agentic-system namespaces
│   ├── target-agentic.yaml          # app-agentic Deployment + Service
│   ├── target-hpa.yaml              # app-hpa Deployment + Service (PodTemplate identical)
│   ├── forecast-service.yaml        # forecast-service Deployment + Service
│   ├── hpa.yaml                     # standard HPA managing app-hpa
│   └── agenticautoscaler-sample.yaml # AgenticAutoscaler CR managing app-agentic
└── grafana/
    ├── agentic-dashboard.json       # 7-panel dashboard
    ├── kustomization.yaml           # configMapGenerator that auto-imports the JSON
    └── dashboard-configmap.yaml     # hand-maintained alternative
```

## Apply

> **Canonical path:** `make kind-up && make kind-load && make install-deps && make deploy`. The Makefile encodes the correct apply order and is what CI uses; the manual sequence below is **reference only** for ad-hoc debugging or running parts of the stack standalone.

The actual command sequence (cluster bring-up, image build, image load,
manifest apply ordering) is owned by Plan #11's Makefile. Quick reference:

```bash
# 1. Cluster
kind create cluster --config deploy/kind/cluster.yaml

# 2. Helm
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo add jetstack https://charts.jetstack.io
helm upgrade --install kube-prom prometheus-community/kube-prometheus-stack \
  -n monitoring --create-namespace -f deploy/helm/prometheus-values.yaml
helm upgrade --install cert-manager jetstack/cert-manager \
  -n cert-manager --create-namespace -f deploy/helm/certmanager-values.yaml

# 3. Build + load images
make docker-build IMG=target-app:latest -C target-app
make docker-build IMG=forecast-service:latest -C forecast-service
make docker-build IMG=controller:latest                  # operator
kind load docker-image target-app:latest forecast-service:latest controller:latest

# 4. Apply manifests
kubectl apply -f deploy/manifests/namespace.yaml
kubectl apply -f deploy/manifests/forecast-service.yaml
kubectl apply -f deploy/manifests/target-agentic.yaml
kubectl apply -f deploy/manifests/target-hpa.yaml
kubectl apply -f deploy/manifests/hpa.yaml
make deploy IMG=controller:latest                        # CRDs + RBAC + controller
kubectl apply -f deploy/manifests/agenticautoscaler-sample.yaml

# 5. Dashboard
kubectl apply -k deploy/grafana/                          # auto-imported by Grafana sidecar
```

## Validation

`go test ./test/smoke/...` validates:

1. Every YAML in `deploy/manifests/` parses cleanly and has
   `apiVersion`, `kind`, `metadata.name`.
2. `target-agentic.yaml` and `target-hpa.yaml` have byte-identical
   `.spec.template.spec` — only `metadata.name`, labels, and selectors
   differ. This is the apples-to-apples invariant for the
   AgenticAutoscaler-vs-HPA comparison.
3. `hpa.yaml` `.spec.scaleTargetRef.name == "app-hpa"`.
4. `agenticautoscaler-sample.yaml` `.spec.targetRef.name == "app-agentic"`.
5. The Grafana dashboard JSON parses, has `uid: agentic-autoscaler`, and
   contains exactly 7 panels.
6. The dashboard ConfigMap carries `grafana_dashboard: "1"` so the
   kube-prometheus-stack sidecar auto-imports it.
7. `kustomization.yaml` generates an `agentic-dashboard` ConfigMap from
   the JSON.
8. `kind/cluster.yaml` has exactly 3 nodes.
9. `prometheus-values.yaml` enables the Grafana sidecar with the right
   label.

External validators (yamllint, kubeconform) optionally available — run
manually:

```bash
yamllint -d relaxed deploy/
kubeconform -strict -summary deploy/manifests/      # AgenticAutoscaler will need -schema-location for the CRD
```
