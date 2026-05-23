# Plan 10 — Cluster + Observability + Manifests Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver all deployment manifests, the kind cluster config, Helm values for kube-prometheus-stack and cert-manager, the Grafana dashboard JSON, the target-app Kubernetes Deployments (app-agentic + app-hpa with identical PodTemplate), the HPA for app-hpa, the AgenticAutoscaler sample CR, the forecast-service Deployment, and `kubeconform` + `yamllint` snapshot validation. This plan makes the project deployable end-to-end on a kind cluster.

**Architecture:** Everything lives under `deploy/`. The kind config uses 3 nodes. Helm values are CI-friendly (no persistence, no alertmanager). The Grafana dashboard is auto-loaded via the kube-prometheus-stack sidecar ConfigMap label (`grafana_dashboard=1`). Target deployments share an identical PodTemplate except `name`/`labels`.

**Tech Stack:** kind v0.24, Helm 3, kube-prometheus-stack (latest stable chart), cert-manager v1.16 (Helm), kubeconform, yamllint, YAML/JSON manifests.

---

## Spec Coverage Map

| Strategy doc section | Tasks |
| --- | --- |
| §4 repo layout: `deploy/kind/cluster.yaml` | T1 |
| §4 repo layout: `deploy/helm/` values | T2, T3 |
| §4 repo layout: `deploy/manifests/` | T4, T5, T6, T7, T8 |
| §4 repo layout: `deploy/grafana/agentic-dashboard.json` | T9 |
| §7.2 Plan 10: kubeconform + yamllint clean | T10 |
| §7.2 Plan 10: dashboard imports via sidecar | T9 |
| §7.2 Plan 10: HPA targets app-hpa | T7 |
| §7.2 Plan 10: AgenticAutoscaler sample CR targets app-agentic | T8 |
| §7.2 Plan 10: PodTemplate diff assertion | T10 |
| §11 R8: byte-identical PodTemplate | T5, T10 |
| §11 R9: dashboard as ConfigMap | T9 |

---

## File Structure

```
scaler/deploy/
├── kind/
│   └── cluster.yaml                 # T1: 3-node kind config
├── helm/
│   ├── prometheus-values.yaml       # T2: CI-friendly kube-prometheus-stack
│   └── certmanager-values.yaml      # T3: cert-manager Helm values
├── manifests/
│   ├── namespace.yaml               # T4: demo + agentic-system namespaces
│   ├── target-agentic.yaml          # T5: Deployment for app-agentic
│   ├── target-hpa.yaml              # T5: Deployment for app-hpa (identical PodTemplate)
│   ├── forecast-service.yaml        # T6: Deployment + Service
│   ├── hpa.yaml                     # T7: HPA targeting app-hpa
│   └── agenticautoscaler-sample.yaml # T8: AgenticAutoscaler CR
└── grafana/
    └── agentic-dashboard.json       # T9: seven-panel Grafana dashboard
```

---

## Phase 1 — Kind + Helm values

### Task 1: Kind cluster config

**Files:**
- Create: `deploy/kind/cluster.yaml`

- [ ] **Step 1: Write cluster.yaml**

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  kubeadmConfigPatches:
  - |
    kind: InitConfiguration
    nodeRegistration:
      kubeletExtraArgs:
        node-labels: "ingress-ready=true"
  extraPortMappings:
  - containerPort: 30080
    hostPort: 30080
    protocol: TCP
- role: worker
- role: worker
```

- [ ] **Step 2: Commit**

```bash
git add deploy/kind/
git commit -m "feat(deploy): 3-node kind cluster config"
```

---

### Task 2: kube-prometheus-stack Helm values

**Files:**
- Create: `deploy/helm/prometheus-values.yaml`

- [ ] **Step 1: Write CI-friendly values**

```yaml
# CI-friendly: disable heavy components to fit 7GB runner.
alertmanager:
  enabled: false
kubeStateMetrics:
  enabled: false
nodeExporter:
  enabled: false
grafana:
  enabled: true
  sidecar:
    dashboards:
      enabled: true
      label: grafana_dashboard
      labelValue: "1"
  persistence:
    enabled: false
  adminPassword: admin
prometheus:
  prometheusSpec:
    retention: 2h
    resources:
      requests:
        memory: 512Mi
      limits:
        memory: 1Gi
    storageSpec: {}
```

- [ ] **Step 2: Commit**

```bash
git add deploy/helm/prometheus-values.yaml
git commit -m "feat(deploy): kube-prometheus-stack Helm values (CI-friendly)"
```

---

### Task 3: cert-manager Helm values

**Files:**
- Create: `deploy/helm/certmanager-values.yaml`

- [ ] **Step 1: Write values**

```yaml
installCRDs: true
resources:
  requests:
    memory: 64Mi
  limits:
    memory: 256Mi
```

- [ ] **Step 2: Commit**

```bash
git add deploy/helm/certmanager-values.yaml
git commit -m "feat(deploy): cert-manager Helm values"
```

---

## Phase 2 — Application manifests

### Task 4: Namespaces

**Files:**
- Create: `deploy/manifests/namespace.yaml`

- [ ] **Step 1: Write namespace.yaml**

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: demo
---
apiVersion: v1
kind: Namespace
metadata:
  name: agentic-system
```

- [ ] **Step 2: Commit**

```bash
git add deploy/manifests/namespace.yaml
git commit -m "feat(deploy): demo + agentic-system namespaces"
```

---

### Task 5: Target deployments (app-agentic + app-hpa)

**Files:**
- Create: `deploy/manifests/target-agentic.yaml`
- Create: `deploy/manifests/target-hpa.yaml`

The two Deployments MUST share an identical PodTemplate (same image, same ports, same resources, same env) — only `metadata.name` and `metadata.labels.app` differ. This ensures the comparison is fair.

- [ ] **Step 1: Write target-agentic.yaml**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app-agentic
  namespace: demo
  labels:
    app: app-agentic
spec:
  replicas: 2
  selector:
    matchLabels:
      app: app-agentic
  template:
    metadata:
      labels:
        app: app-agentic
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "8080"
        prometheus.io/path: "/metrics"
    spec:
      containers:
      - name: target-app
        image: target-app:latest
        ports:
        - containerPort: 8080
          name: http
        env:
        - name: PORT
          value: "8080"
        - name: CONCURRENCY_LIMIT
          value: "50"
        - name: WORK_DURATION_MS
          value: "100"
        resources:
          requests:
            cpu: 100m
            memory: 64Mi
          limits:
            cpu: 500m
            memory: 128Mi
        readinessProbe:
          httpGet:
            path: /readyz
            port: http
          initialDelaySeconds: 2
          periodSeconds: 5
        livenessProbe:
          httpGet:
            path: /healthz
            port: http
          initialDelaySeconds: 5
          periodSeconds: 10
---
apiVersion: v1
kind: Service
metadata:
  name: app-agentic
  namespace: demo
  labels:
    app: app-agentic
spec:
  selector:
    app: app-agentic
  ports:
  - port: 80
    targetPort: http
    name: http
```

- [ ] **Step 2: Write target-hpa.yaml (identical PodTemplate, different name/labels)**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app-hpa
  namespace: demo
  labels:
    app: app-hpa
spec:
  replicas: 2
  selector:
    matchLabels:
      app: app-hpa
  template:
    metadata:
      labels:
        app: app-hpa
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "8080"
        prometheus.io/path: "/metrics"
    spec:
      containers:
      - name: target-app
        image: target-app:latest
        ports:
        - containerPort: 8080
          name: http
        env:
        - name: PORT
          value: "8080"
        - name: CONCURRENCY_LIMIT
          value: "50"
        - name: WORK_DURATION_MS
          value: "100"
        resources:
          requests:
            cpu: 100m
            memory: 64Mi
          limits:
            cpu: 500m
            memory: 128Mi
        readinessProbe:
          httpGet:
            path: /readyz
            port: http
          initialDelaySeconds: 2
          periodSeconds: 5
        livenessProbe:
          httpGet:
            path: /healthz
            port: http
          initialDelaySeconds: 5
          periodSeconds: 10
---
apiVersion: v1
kind: Service
metadata:
  name: app-hpa
  namespace: demo
  labels:
    app: app-hpa
spec:
  selector:
    app: app-hpa
  ports:
  - port: 80
    targetPort: http
    name: http
```

- [ ] **Step 3: Commit**

```bash
git add deploy/manifests/target-agentic.yaml deploy/manifests/target-hpa.yaml
git commit -m "feat(deploy): target deployments (app-agentic + app-hpa, identical PodTemplate)"
```

---

### Task 6: Forecast Service deployment

**Files:**
- Create: `deploy/manifests/forecast-service.yaml`

- [ ] **Step 1: Write forecast-service.yaml**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: forecast-service
  namespace: agentic-system
  labels:
    app: forecast-service
spec:
  replicas: 1
  selector:
    matchLabels:
      app: forecast-service
  template:
    metadata:
      labels:
        app: forecast-service
    spec:
      containers:
      - name: forecast-service
        image: forecast-service:latest
        ports:
        - containerPort: 8000
          name: http
        env:
        - name: FORECAST_HORIZON_MINUTES
          value: "10"
        - name: PROPHET_MIN_POINTS
          value: "60"
        resources:
          requests:
            cpu: 200m
            memory: 512Mi
          limits:
            cpu: "1"
            memory: 1Gi
        readinessProbe:
          httpGet:
            path: /healthz
            port: http
          initialDelaySeconds: 30
          periodSeconds: 10
        livenessProbe:
          httpGet:
            path: /healthz
            port: http
          initialDelaySeconds: 60
          periodSeconds: 30
---
apiVersion: v1
kind: Service
metadata:
  name: forecast-service
  namespace: agentic-system
  labels:
    app: forecast-service
spec:
  selector:
    app: forecast-service
  ports:
  - port: 80
    targetPort: http
    name: http
```

- [ ] **Step 2: Commit**

```bash
git add deploy/manifests/forecast-service.yaml
git commit -m "feat(deploy): forecast-service Deployment + Service"
```

---

### Task 7: HPA for app-hpa

**Files:**
- Create: `deploy/manifests/hpa.yaml`

- [ ] **Step 1: Write hpa.yaml**

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: app-hpa
  namespace: demo
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: app-hpa
  minReplicas: 2
  maxReplicas: 10
  metrics:
  - type: Pods
    pods:
      metric:
        name: http_requests_per_second
      target:
        type: AverageValue
        averageValue: "200"
  behavior:
    scaleUp:
      stabilizationWindowSeconds: 60
      policies:
      - type: Pods
        value: 4
        periodSeconds: 60
    scaleDown:
      stabilizationWindowSeconds: 300
      policies:
      - type: Pods
        value: 2
        periodSeconds: 60
```

- [ ] **Step 2: Commit**

```bash
git add deploy/manifests/hpa.yaml
git commit -m "feat(deploy): HPA targeting app-hpa with comparable scaling params"
```

---

### Task 8: AgenticAutoscaler sample CR

**Files:**
- Create: `deploy/manifests/agenticautoscaler-sample.yaml`

- [ ] **Step 1: Write sample CR matching design §4**

```yaml
apiVersion: autoscaling.agentic.io/v1alpha1
kind: AgenticAutoscaler
metadata:
  name: app-agentic
  namespace: demo
  annotations:
    autoscaling.agentic.io/kill-switch: "false"
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: app-agentic
  minReplicas: 2
  maxReplicas: 10
  rpsPerPodMin: 50
  rpsPerPodMax: 500
```

- [ ] **Step 2: Commit**

```bash
git add deploy/manifests/agenticautoscaler-sample.yaml
git commit -m "feat(deploy): AgenticAutoscaler sample CR targeting app-agentic"
```

---

## Phase 3 — Grafana dashboard

### Task 9: Seven-panel dashboard JSON

**Files:**
- Create: `deploy/grafana/agentic-dashboard.json`
- Create: `deploy/grafana/dashboard-configmap.yaml`

- [ ] **Step 1: Write the dashboard JSON**

The dashboard contains seven panels per the strategy doc:

1. **Current RPS** (both targets) — `sum(rate(http_requests_total{app=~"app-agentic|app-hpa"}[2m])) by (app)`
2. **Replica Count** — `kube_deployment_spec_replicas{deployment=~"app-agentic|app-hpa"}`
3. **Predicted RPS** — custom metric from controller (or from the CR status via kube-state-metrics custom resource metrics, future)
4. **p99 Latency** — `histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket{app=~"app-agentic|app-hpa"}[2m])) by (le, app))`
5. **5xx Rate** — `sum(rate(http_requests_total{app=~"app-agentic|app-hpa", status=~"5.."}[2m])) by (app)`
6. **Scaling Events** — K8s events (displayed as annotations via Grafana's events datasource or a table panel from the controller's metrics)
7. **Classification** — `status.classifiedParams.pattern` (via a stat panel showing the last known pattern)

The full JSON is a standard Grafana dashboard export (~300 lines). Key structure:

```json
{
  "dashboard": {
    "title": "Agentic Autoscaler",
    "uid": "agentic-autoscaler",
    "panels": [
      { "title": "Current RPS", "type": "timeseries", ... },
      { "title": "Replica Count", "type": "timeseries", ... },
      { "title": "Predicted RPS", "type": "timeseries", ... },
      { "title": "p99 Latency", "type": "timeseries", ... },
      { "title": "5xx Rate", "type": "timeseries", ... },
      { "title": "Scaling Events", "type": "table", ... },
      { "title": "Classification", "type": "stat", ... }
    ],
    "templating": { "list": [] },
    "time": { "from": "now-1h", "to": "now" },
    "refresh": "10s"
  }
}
```

(Full panel definitions with PromQL queries, panel positions, colors, and thresholds are included in the actual file.)

- [ ] **Step 2: Write dashboard-configmap.yaml**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: agentic-dashboard
  namespace: monitoring
  labels:
    grafana_dashboard: "1"
data:
  agentic-dashboard.json: |
    <contents of agentic-dashboard.json>
```

In practice, the ConfigMap's `data` field will reference the JSON file content. For the plan, the task is to create the ConfigMap that triggers the kube-prometheus-stack Grafana sidecar to auto-load it.

- [ ] **Step 3: Commit**

```bash
git add deploy/grafana/
git commit -m "feat(deploy): Grafana dashboard (7 panels) + sidecar ConfigMap"
```

---

## Phase 4 — Validation + milestone

### Task 10: kubeconform + yamllint + PodTemplate diff

**Files:**
- Create: `test/smoke/manifest_test.go` (or shell script)

- [ ] **Step 1: Run yamllint on all YAML**

```bash
yamllint -d relaxed deploy/manifests/ deploy/kind/ deploy/helm/
```

Expected: clean.

- [ ] **Step 2: Run kubeconform**

```bash
kubeconform -strict -summary deploy/manifests/
```

Expected: all resources valid. (The AgenticAutoscaler CR will fail since the CRD schema isn't in kubeconform's defaults — skip it or provide the CRD schema via `-schema-location`.)

- [ ] **Step 3: PodTemplate diff assertion**

```bash
# Extract PodTemplate specs and diff them (only name/labels should differ).
diff <(yq '.spec.template.spec' deploy/manifests/target-agentic.yaml) \
     <(yq '.spec.template.spec' deploy/manifests/target-hpa.yaml)
```

Expected: identical output (no diff). The only differences are in `.metadata.name`, `.metadata.labels`, `.spec.selector`, and `.spec.template.metadata.labels` — not in `.spec.template.spec`.

- [ ] **Step 4: Commit validation script**

```bash
git add test/smoke/
git commit -m "test(deploy): kubeconform + yamllint + PodTemplate diff assertion"
```

---

### Task 11: Milestone commit

- [ ] **Step 1: Milestone**

```bash
git commit --allow-empty -m "milestone: Plan #10 (cluster + observability + manifests) complete

deploy/kind/:
- 3-node kind cluster config (control-plane + 2 workers)

deploy/helm/:
- kube-prometheus-stack values (CI-friendly: no alertmanager, no kube-state-metrics,
  grafana sidecar enabled for auto-dashboard-import)
- cert-manager values (installCRDs: true)

deploy/manifests/:
- demo + agentic-system namespaces
- app-agentic Deployment + Service (target for AgenticAutoscaler)
- app-hpa Deployment + Service (identical PodTemplate; target for standard HPA)
- forecast-service Deployment + Service (1Gi memory limit for Prophet)
- HPA targeting app-hpa (minReplicas=2, maxReplicas=10, comparable params)
- AgenticAutoscaler sample CR targeting app-agentic

deploy/grafana/:
- Seven-panel Grafana dashboard JSON (RPS, replicas, predicted, p99, 5xx, events, pattern)
- ConfigMap with grafana_dashboard=1 label for sidecar auto-load

Validation:
- yamllint clean on all YAML
- kubeconform strict on all manifests (minus custom CRD)
- PodTemplate spec diff assertion: both targets are byte-identical in .spec.template.spec
"
```

---

## Plan-specific Definition of Done

- [ ] `yamllint -d relaxed deploy/` passes clean.
- [ ] `kubeconform -strict -summary deploy/manifests/` reports all standard resources valid.
- [ ] PodTemplate `.spec.template.spec` is identical between `target-agentic.yaml` and `target-hpa.yaml`.
- [ ] Dashboard JSON parses as valid JSON and contains exactly 7 panels.
- [ ] Dashboard ConfigMap has `grafana_dashboard: "1"` label.
- [ ] HPA `.spec.scaleTargetRef` points to `app-hpa` Deployment.
- [ ] AgenticAutoscaler sample CR `.spec.targetRef` points to `app-agentic` Deployment.
- [ ] `kind create cluster --config deploy/kind/cluster.yaml` succeeds (manual local verification).

---

## Notes on what's intentionally deferred

- **Helm install commands** — Plan #11's Makefile (`make install-deps`).
- **Image building and kind-loading** — Plan #11's Makefile (`make images`, `make kind-load`).
- **Applying manifests in dependency order** — Plan #11's Makefile (`make deploy`).
- **Controller RBAC / webhook manifests** — already generated by kubebuilder in `config/` (Plans #1, #2). This plan only covers `deploy/`.
- **ServiceMonitor for custom metrics** — out of scope; Prometheus scrapes via pod annotations.

---

## Self-Review

**Spec coverage.** Every item in strategy §7.1 Plan 10 and §7.2 Plan 10 gates is covered.

**Placeholders.** The dashboard JSON body is described structurally (7 panels with PromQL); the actual implementation will be a full Grafana export. This is acceptable because the dashboard is Tier-3 (snapshot/lint only) and the important assertion is that it imports and has 7 panels.

**Type consistency.** Deployment names (`app-agentic`, `app-hpa`, `forecast-service`) match across manifests, the HPA `scaleTargetRef`, the AgenticAutoscaler CR `targetRef`, k6 scenario env vars (`TARGET_AGENTIC_URL`, `TARGET_HPA_URL`), and the design spec §4.

---

## Execution handoff

Plan complete. Two execution options:

1. **Subagent-Driven (recommended)**
2. **Inline Execution**

Which approach?
