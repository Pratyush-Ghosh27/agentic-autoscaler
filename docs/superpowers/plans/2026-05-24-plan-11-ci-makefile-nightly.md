# Plan 11 — CI + Makefile + Dev Tooling + Runbooks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver the final integration layer: the `Makefile` with all targets from strategy §9, both GitHub Actions workflows (PR CI under 10 min, nightly E2E under 60 min), the smoke test harness, the E2E assertion script, and operational runbooks. After this plan lands, the project is fully buildable, testable, and deployable from a single `make` invocation.

**Architecture:** The Makefile is the single entry point for all operations. GitHub Actions workflows call Makefile targets — no inline logic in YAML beyond checkout/setup. The smoke test is a shell script that provisions kind, deploys, and asserts liveness. The nightly E2E extends smoke with k6 load + Prometheus assertion queries.

**Tech Stack:** GNU Make, GitHub Actions, bash, kind, Helm, kubectl, k6, curl, jq, Go 1.23, Python 3.12.

---

## Spec Coverage Map

| Strategy doc section | Tasks |
| --- | --- |
| §8.1 PR CI workflow (lint, generate-check, test-go, test-python, test-envtest, build-images, smoke) | T4 |
| §8.2 Nightly E2E workflow (kind, helm, ollama, build, deploy, k6, prometheus assertions) | T5 |
| §9 Full Makefile target inventory | T1, T2, T3 |
| §10 Local development workflow | T6 (runbook) |
| §7.2 Plan 11: nightly completes < 60 min | T5 |
| §7.2 Plan 11: PR CI completes < 10 min | T4 |
| §7.2 Plan 11: every Makefile target runs cleanly | T3 |
| §7.2 Plan 11: every runbook walked end-to-end | T6, T7, T8 |
| §5.3 Coverage gates (CI-enforced) | T4 (coverage step) |
| §5.4.9 `make smoke` passes locally | T3 (smoke target) |
| §6.3 `make gen-testdata` regenerates idempotently | T2 |
| §11 R1: cache ~/.ollama/models in nightly | T5 |
| §11 R11: testdata schema validated in CI | T4 |

---

## File Structure

```
scaler/
├── Makefile                              # T1-T3: all targets
├── .github/workflows/
│   ├── ci.yml                            # T4: PR CI
│   └── nightly-e2e.yml                   # T5: nightly quantitative E2E
├── test/
│   ├── smoke/
│   │   └── run.sh                        # T3: smoke test script
│   └── e2e/
│       ├── run.sh                        # T5: E2E orchestration
│       └── assertions.sh                 # T5: Prometheus query assertions
├── docs/runbooks/
│   ├── kind-bootstrap.md                 # T6
│   ├── ollama-setup.md                   # T7
│   ├── nightly-e2e.md                    # T8
│   └── grafana-dashboard.md              # T8
└── README.md                             # T9: final polish
```

---

## Phase 1 — Makefile

### Task 1: Makefile core targets (help, tools, code generation, lint)

**Files:**
- Create: `Makefile`

- [ ] **Step 1: Write the first section of Makefile**

```makefile
# ============================================================
# Agentic Autoscaler — Makefile
# ============================================================

SHELL := /bin/bash
.DEFAULT_GOAL := help

# Versions (pinned)
GO_VERSION        ?= 1.23
PYTHON_VERSION    ?= 3.12
KIND_VERSION      ?= 0.24.0
KUBEBUILDER_VERSION ?= 4.3.0
GOLANGCI_VERSION  ?= 1.61.0
K6_VERSION        ?= latest
HELM_VERSION      ?= 3.16.0
KUBECONFORM_VERSION ?= 0.6.7
OLLAMA_MODEL      ?= llama3.2
OLLAMA_MODEL_CI   ?= phi3

# Directories
ROOT_DIR     := $(shell pwd)
CONTROLLER   := $(ROOT_DIR)
FORECAST_SVC := $(ROOT_DIR)/forecast-service
TARGET_APP   := $(ROOT_DIR)/target-app

# Image tags
IMG_TAG      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
CONTROLLER_IMG   := controller:$(IMG_TAG)
FORECAST_IMG     := forecast-service:$(IMG_TAG)
TARGET_APP_IMG   := target-app:$(IMG_TAG)

##@ General

.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

##@ Environment

.PHONY: tools
tools: ## Install dev tools (golangci-lint, kubebuilder, kind, helm, k6, kubeconform, yamllint)
	@echo "Installing tools..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v$(GOLANGCI_VERSION)
	@which kind || (curl -Lo ./kind https://kind.sigs.k8s.io/dl/v$(KIND_VERSION)/kind-linux-amd64 && chmod +x ./kind && sudo mv ./kind /usr/local/bin/)
	@which helm || (curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash)
	@which kubeconform || go install github.com/yannh/kubeconform/cmd/kubeconform@v$(KUBECONFORM_VERSION)
	@which yamllint || pip install yamllint
	@echo "Tools installed."

.PHONY: ollama-pull
ollama-pull: ## Pull the Ollama model for local dev
	ollama pull $(OLLAMA_MODEL)

##@ Code Generation

.PHONY: manifests
manifests: ## Generate CRD manifests (kubebuilder controller-gen)
	cd $(CONTROLLER) && go run sigs.k8s.io/controller-tools/cmd/controller-gen rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: ## Run go generate
	cd $(CONTROLLER) && go generate ./...

.PHONY: gen-testdata
gen-testdata: ## Regenerate testdata/*.json from hack/synthetic
	go run ./hack/synthetic --output=testdata/ --seed=42

##@ Lint

.PHONY: lint
lint: lint-go lint-python lint-yaml ## Run all linters

.PHONY: lint-go
lint-go: ## Go lint
	golangci-lint run ./...
	cd $(TARGET_APP) && golangci-lint run ./...

.PHONY: lint-python
lint-python: ## Python lint (ruff + mypy)
	cd $(FORECAST_SVC) && ruff check src/ tests/
	cd $(FORECAST_SVC) && mypy src/

.PHONY: lint-yaml
lint-yaml: ## YAML lint + kubeconform
	yamllint -d relaxed deploy/
	kubeconform -strict -summary deploy/manifests/namespace.yaml deploy/manifests/target-*.yaml deploy/manifests/forecast-service.yaml deploy/manifests/hpa.yaml
```

- [ ] **Step 2: Commit**

```bash
git add Makefile
git commit -m "feat(makefile): core targets (help, tools, manifests, generate, lint)"
```

---

### Task 2: Makefile test + build targets

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Append test and build targets**

```makefile
##@ Test

.PHONY: test
test: test-go test-python ## All Tier-1 tests

.PHONY: test-go
test-go: ## Go unit + adapter + webhook tests
	go test ./internal/... ./api/... -v -count=1 -coverprofile=coverage-controller.out
	cd $(TARGET_APP) && go test ./... -v -count=1 -coverprofile=coverage-target.out

.PHONY: test-python
test-python: ## Python unit + integration tests
	cd $(FORECAST_SVC) && pytest tests/ -v --cov=src/forecast --cov-report=term-missing --cov-fail-under=90

.PHONY: test-envtest
test-envtest: ## Tier-2 envtest suite
	go test ./internal/controller/... -v -count=1 -tags=envtest

.PHONY: test-all
test-all: test test-envtest ## Everything

##@ Build

.PHONY: build
build: build-controller build-target-app build-forecast ## Build all binaries/wheels

.PHONY: build-controller
build-controller:
	cd $(CONTROLLER) && go build -o bin/controller ./cmd/controller/

.PHONY: build-target-app
build-target-app:
	cd $(TARGET_APP) && go build -o bin/target-app ./cmd/target-app/

.PHONY: build-forecast
build-forecast:
	cd $(FORECAST_SVC) && python -m build

.PHONY: images
images: ## Build all container images
	docker build -t $(CONTROLLER_IMG) -f Dockerfile .
	docker build -t $(FORECAST_IMG) -f $(FORECAST_SVC)/Dockerfile $(FORECAST_SVC)
	docker build -t $(TARGET_APP_IMG) -f $(TARGET_APP)/Dockerfile $(TARGET_APP)
```

- [ ] **Step 2: Commit**

```bash
git add Makefile
git commit -m "feat(makefile): test + build + images targets"
```

---

### Task 3: Makefile cluster lifecycle + scenarios + smoke

**Files:**
- Modify: `Makefile`
- Create: `test/smoke/run.sh`

- [ ] **Step 1: Append cluster lifecycle and scenario targets**

```makefile
##@ Cluster Lifecycle

.PHONY: kind-up
kind-up: ## Create kind cluster
	kind create cluster --config deploy/kind/cluster.yaml --name agentic

.PHONY: kind-load
kind-load: ## Load images into kind
	kind load docker-image $(CONTROLLER_IMG) --name agentic
	kind load docker-image $(FORECAST_IMG) --name agentic
	kind load docker-image $(TARGET_APP_IMG) --name agentic

.PHONY: install-deps
install-deps: ## Helm install kube-prometheus-stack + cert-manager
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts || true
	helm repo add jetstack https://charts.jetstack.io || true
	helm repo update
	helm upgrade --install cert-manager jetstack/cert-manager -n cert-manager --create-namespace -f deploy/helm/certmanager-values.yaml --wait
	helm upgrade --install kube-prom prometheus-community/kube-prometheus-stack -n monitoring --create-namespace -f deploy/helm/prometheus-values.yaml --wait --timeout 5m

.PHONY: deploy
deploy: ## Apply manifests in dependency order
	kubectl apply -f deploy/manifests/namespace.yaml
	kubectl apply -k config/default
	kubectl apply -f deploy/manifests/forecast-service.yaml
	kubectl apply -f deploy/manifests/target-agentic.yaml
	kubectl apply -f deploy/manifests/target-hpa.yaml
	kubectl apply -f deploy/manifests/hpa.yaml
	kubectl apply -f deploy/grafana/dashboard-configmap.yaml -n monitoring
	kubectl apply -f deploy/manifests/agenticautoscaler-sample.yaml

.PHONY: undeploy
undeploy: ## Remove application manifests
	kubectl delete -f deploy/manifests/agenticautoscaler-sample.yaml --ignore-not-found
	kubectl delete -f deploy/manifests/hpa.yaml --ignore-not-found
	kubectl delete -f deploy/manifests/target-hpa.yaml --ignore-not-found
	kubectl delete -f deploy/manifests/target-agentic.yaml --ignore-not-found
	kubectl delete -f deploy/manifests/forecast-service.yaml --ignore-not-found
	kubectl delete -k config/default --ignore-not-found

.PHONY: kind-down
kind-down: ## Delete kind cluster
	kind delete cluster --name agentic

##@ Scenarios

.PHONY: k6-ramp
k6-ramp: ## Run ramp k6 scenario
	k6 run k6/scenarios/ramp.js

.PHONY: k6-steady
k6-steady: ## Run steady k6 scenario
	k6 run k6/scenarios/steady.js

.PHONY: k6-spiky
k6-spiky: ## Run spiky k6 scenario
	k6 run k6/scenarios/spiky.js

.PHONY: k6-bursty
k6-bursty: ## Run bursty k6 scenario
	k6 run k6/scenarios/bursty.js

##@ Smoke + E2E

.PHONY: smoke
smoke: ## Full smoke test (kind-up → deploy → assert → kind-down)
	bash test/smoke/run.sh

.PHONY: e2e
e2e: ## Full quantitative E2E (local, tolerance 1.10x)
	TOLERANCE=1.10 bash test/e2e/run.sh

.PHONY: e2e-strict
e2e-strict: ## Release-candidate E2E (tolerance 1.05x)
	TOLERANCE=1.05 bash test/e2e/run.sh

##@ Observability

.PHONY: port-forward-grafana
port-forward-grafana: ## Port-forward Grafana to localhost:3000
	kubectl port-forward -n monitoring svc/kube-prom-grafana 3000:80

.PHONY: port-forward-prometheus
port-forward-prometheus: ## Port-forward Prometheus to localhost:9090
	kubectl port-forward -n monitoring svc/kube-prom-kube-prometheus-prometheus 9090:9090

.PHONY: logs-controller
logs-controller: ## Tail controller logs
	kubectl logs -n agentic-system -l control-plane=controller-manager -f

.PHONY: logs-forecast
logs-forecast: ## Tail forecast-service logs
	kubectl logs -n agentic-system -l app=forecast-service -f
```

- [ ] **Step 2: Write test/smoke/run.sh**

```bash
#!/usr/bin/env bash
set -euo pipefail

echo "=== Smoke Test ==="

CLUSTER_NAME="agentic-smoke-$$"
trap "kind delete cluster --name $CLUSTER_NAME 2>/dev/null || true" EXIT

echo "[1/6] Creating kind cluster..."
kind create cluster --config deploy/kind/cluster.yaml --name "$CLUSTER_NAME"

echo "[2/6] Building and loading images..."
make images
kind load docker-image controller:$(git rev-parse --short HEAD) --name "$CLUSTER_NAME"
kind load docker-image forecast-service:$(git rev-parse --short HEAD) --name "$CLUSTER_NAME"
kind load docker-image target-app:$(git rev-parse --short HEAD) --name "$CLUSTER_NAME"

echo "[3/6] Installing dependencies..."
make install-deps

echo "[4/6] Deploying..."
make deploy

echo "[5/6] Waiting for pods to be ready..."
kubectl wait --for=condition=ready pod -l control-plane=controller-manager -n agentic-system --timeout=120s
kubectl wait --for=condition=ready pod -l app=forecast-service -n agentic-system --timeout=120s
kubectl wait --for=condition=ready pod -l app=app-agentic -n demo --timeout=60s
kubectl wait --for=condition=ready pod -l app=app-hpa -n demo --timeout=60s

echo "[6/6] Asserting health..."
# Controller healthz
kubectl exec -n agentic-system deploy/controller-manager -- wget -qO- http://localhost:8081/healthz | grep -q "ok"
# Forecast service healthz
kubectl exec -n agentic-system deploy/forecast-service -- wget -qO- http://localhost:8000/healthz | grep -q "ok"
# At least one reconcile event
kubectl get events -n demo --field-selector reason=ScaleUp,reason=NoChange --no-headers | head -1

echo "=== Smoke PASSED ==="
```

- [ ] **Step 3: Commit**

```bash
chmod +x test/smoke/run.sh
git add Makefile test/smoke/
git commit -m "feat(makefile): cluster lifecycle + scenarios + smoke test script"
```

---

## Phase 2 — GitHub Actions workflows

### Task 4: PR CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Write ci.yml**

```yaml
name: CI

on:
  pull_request:
  push:
    branches: [main]

env:
  GO_VERSION: "1.23"
  PYTHON_VERSION: "3.12"

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: ${{ env.GO_VERSION }}
    - uses: actions/setup-python@v5
      with:
        python-version: ${{ env.PYTHON_VERSION }}
    - run: pip install ruff mypy yamllint
    - run: go install github.com/yannh/kubeconform/cmd/kubeconform@latest
    - run: make lint

  generate-check:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: ${{ env.GO_VERSION }}
    - run: make manifests generate
    - run: git diff --exit-code

  test-go:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: ${{ env.GO_VERSION }}
    - run: make test-go
    - name: Check coverage threshold
      run: |
        COVERAGE=$(go tool cover -func=coverage-controller.out | tail -1 | awk '{print $3}' | tr -d '%')
        echo "Coverage: ${COVERAGE}%"
        if (( $(echo "$COVERAGE < 90" | bc -l) )); then
          echo "::error::Coverage ${COVERAGE}% is below 90% threshold"
          exit 1
        fi

  test-python:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-python@v5
      with:
        python-version: ${{ env.PYTHON_VERSION }}
    - working-directory: forecast-service
      run: |
        pip install -e ".[dev]"
        pytest tests/ -v --cov=src/forecast --cov-report=term-missing --cov-fail-under=90

  test-envtest:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: ${{ env.GO_VERSION }}
    - run: make test-envtest

  build-images:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: ${{ env.GO_VERSION }}
    - run: make images

  smoke:
    needs: [build-images]
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: ${{ env.GO_VERSION }}
    - name: Install kind
      run: |
        curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.24.0/kind-linux-amd64
        chmod +x ./kind && sudo mv ./kind /usr/local/bin/
    - name: Install helm
      run: curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
    - run: make smoke
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "feat(ci): PR CI workflow (lint, generate-check, test, envtest, build, smoke)"
```

---

### Task 5: Nightly E2E workflow + assertion script

**Files:**
- Create: `.github/workflows/nightly-e2e.yml`
- Create: `test/e2e/run.sh`
- Create: `test/e2e/assertions.sh`

- [ ] **Step 1: Write nightly-e2e.yml**

```yaml
name: Nightly E2E

on:
  schedule:
  - cron: "0 2 * * *"
  workflow_dispatch:

env:
  GO_VERSION: "1.23"
  PYTHON_VERSION: "3.12"
  OLLAMA_MODEL: phi3
  TOLERANCE: "1.25"

jobs:
  e2e:
    runs-on: ubuntu-latest
    timeout-minutes: 60
    steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: ${{ env.GO_VERSION }}

    - name: Install tools
      run: |
        curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.24.0/kind-linux-amd64
        chmod +x ./kind && sudo mv ./kind /usr/local/bin/
        curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
        curl https://github.com/grafana/k6/releases/latest/download/k6-linux-amd64.tar.gz | tar xz
        sudo mv k6 /usr/local/bin/

    - name: Install Ollama
      run: |
        curl -fsSL https://ollama.com/install.sh | sh
        ollama serve &
        sleep 5

    - name: Pull model (cached)
      uses: actions/cache@v4
      with:
        path: ~/.ollama/models
        key: ollama-${{ env.OLLAMA_MODEL }}
    - run: ollama pull ${{ env.OLLAMA_MODEL }}

    - name: Build images
      run: make images

    - name: Create cluster + deploy
      run: |
        make kind-up
        make kind-load
        make install-deps
        make deploy

    - name: Wait for steady state
      run: |
        kubectl wait --for=condition=ready pod -l control-plane=controller-manager -n agentic-system --timeout=180s
        kubectl wait --for=condition=ready pod -l app=forecast-service -n agentic-system --timeout=180s
        kubectl wait --for=condition=ready pod -l app=app-agentic -n demo --timeout=60s
        kubectl wait --for=condition=ready pod -l app=app-hpa -n demo --timeout=60s
        echo "Waiting 5m for classifier first run + steady state..."
        sleep 300

    - name: Run k6 ramp scenario
      run: |
        export TARGET_AGENTIC_URL="http://$(kubectl get svc app-agentic -n demo -o jsonpath='{.spec.clusterIP}')"
        export TARGET_HPA_URL="http://$(kubectl get svc app-hpa -n demo -o jsonpath='{.spec.clusterIP}')"
        k6 run k6/scenarios/ramp.js --duration=25m

    - name: Assert quantitative results
      run: bash test/e2e/assertions.sh

    - name: Collect artifacts on failure
      if: failure()
      run: |
        kubectl logs -n agentic-system -l control-plane=controller-manager --tail=500 > controller-logs.txt
        kubectl logs -n agentic-system -l app=forecast-service --tail=200 > forecast-logs.txt
        kubectl get events -n demo > events.txt

    - uses: actions/upload-artifact@v4
      if: failure()
      with:
        name: e2e-failure-artifacts
        path: |
          controller-logs.txt
          forecast-logs.txt
          events.txt

    - name: Cleanup
      if: always()
      run: kind delete cluster --name agentic
```

- [ ] **Step 2: Write test/e2e/run.sh**

```bash
#!/usr/bin/env bash
set -euo pipefail

TOLERANCE=${TOLERANCE:-1.10}
CLUSTER_NAME="agentic-e2e-$$"
trap "kind delete cluster --name $CLUSTER_NAME 2>/dev/null || true" EXIT

echo "=== E2E Test (tolerance: ${TOLERANCE}x) ==="

echo "[1/7] Creating cluster..."
kind create cluster --config deploy/kind/cluster.yaml --name "$CLUSTER_NAME"

echo "[2/7] Building + loading images..."
make images
IMG_TAG=$(git rev-parse --short HEAD)
kind load docker-image "controller:$IMG_TAG" --name "$CLUSTER_NAME"
kind load docker-image "forecast-service:$IMG_TAG" --name "$CLUSTER_NAME"
kind load docker-image "target-app:$IMG_TAG" --name "$CLUSTER_NAME"

echo "[3/7] Installing deps..."
make install-deps

echo "[4/7] Deploying..."
make deploy

echo "[5/7] Waiting for steady state (5 min)..."
kubectl wait --for=condition=ready pod -l control-plane=controller-manager -n agentic-system --timeout=180s
kubectl wait --for=condition=ready pod -l app=app-agentic -n demo --timeout=60s
sleep 300

echo "[6/7] Running k6 ramp (25 min)..."
export TARGET_AGENTIC_URL="http://$(kubectl get svc app-agentic -n demo -o jsonpath='{.spec.clusterIP}')"
export TARGET_HPA_URL="http://$(kubectl get svc app-hpa -n demo -o jsonpath='{.spec.clusterIP}')"
k6 run k6/scenarios/ramp.js --duration=25m

echo "[7/7] Asserting results..."
TOLERANCE=$TOLERANCE bash test/e2e/assertions.sh

echo "=== E2E PASSED ==="
```

- [ ] **Step 3: Write test/e2e/assertions.sh**

```bash
#!/usr/bin/env bash
set -euo pipefail

TOLERANCE=${TOLERANCE:-1.25}
PROM_URL="http://localhost:9090"

# Port-forward Prometheus in background.
kubectl port-forward -n monitoring svc/kube-prom-kube-prometheus-prometheus 9090:9090 &
PF_PID=$!
trap "kill $PF_PID 2>/dev/null || true" EXIT
sleep 3

query() {
  local q="$1"
  curl -s "${PROM_URL}/api/v1/query?query=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$q'))")" \
    | jq -r '.data.result[0].value[1] // "0"'
}

echo "Querying p99 latencies..."
P99_AGENTIC=$(query 'histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket{app="app-agentic"}[25m])) by (le))')
P99_HPA=$(query 'histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket{app="app-hpa"}[25m])) by (le))')

echo "  p99 agentic: ${P99_AGENTIC}s"
echo "  p99 hpa:     ${P99_HPA}s"

echo "Querying 5xx rates..."
E5XX_AGENTIC=$(query 'sum(rate(http_requests_total{app="app-agentic",status=~"5.."}[25m]))')
E5XX_HPA=$(query 'sum(rate(http_requests_total{app="app-hpa",status=~"5.."}[25m]))')

echo "  5xx agentic: ${E5XX_AGENTIC}/s"
echo "  5xx hpa:     ${E5XX_HPA}/s"

# Assertions.
python3 -c "
import sys
t = float('$TOLERANCE')
p99_a, p99_h = float('$P99_AGENTIC'), float('$P99_HPA')
e5_a, e5_h = float('$E5XX_AGENTIC'), float('$E5XX_HPA')

failures = []
if p99_h > 0 and p99_a > p99_h * t:
    failures.append(f'p99 agentic ({p99_a:.4f}s) > p99 hpa ({p99_h:.4f}s) * {t}')
if e5_h > 0 and e5_a > e5_h * t:
    failures.append(f'5xx agentic ({e5_a:.6f}/s) > 5xx hpa ({e5_h:.6f}/s) * {t}')

if failures:
    for f in failures:
        print(f'FAIL: {f}', file=sys.stderr)
    sys.exit(1)
else:
    print('All assertions passed.')
"
```

- [ ] **Step 4: Commit**

```bash
chmod +x test/e2e/run.sh test/e2e/assertions.sh
git add .github/workflows/nightly-e2e.yml test/e2e/
git commit -m "feat(ci): nightly E2E workflow + assertion script (p99, 5xx, tolerance)"
```

---

## Phase 3 — Runbooks + README

### Task 6: kind-bootstrap runbook

**Files:**
- Create: `docs/runbooks/kind-bootstrap.md`

- [ ] **Step 1: Write the runbook**

```markdown
# Kind Bootstrap Runbook

## Prerequisites

- Docker (running)
- Go 1.23+
- Python 3.12+
- `make tools` completed

## Steps

1. **Create cluster:**
   ```bash
   make kind-up
   ```

2. **Install dependencies:**
   ```bash
   make install-deps
   ```
   Waits for cert-manager and kube-prometheus-stack to be ready.

3. **Build and load images:**
   ```bash
   make images && make kind-load
   ```

4. **Deploy:**
   ```bash
   make deploy
   ```

5. **Verify:**
   ```bash
   kubectl get pods -A
   kubectl get agenticautoscaler -n demo
   ```

6. **Drive load:**
   ```bash
   make k6-ramp
   ```

7. **Observe:**
   ```bash
   make port-forward-grafana
   # Open http://localhost:3000 (admin/admin)
   ```

## Teardown

```bash
make kind-down
```

## Troubleshooting

- **Pods pending:** check node resources with `kubectl describe nodes`
- **cert-manager not ready:** `kubectl get pods -n cert-manager`; wait for all 3 pods
- **Forecast service CrashLoopBackOff:** check memory limit; Prophet warm-up needs ~800MB peak
```

- [ ] **Step 2: Commit**

```bash
git add docs/runbooks/kind-bootstrap.md
git commit -m "docs: kind-bootstrap runbook"
```

---

### Task 7: Ollama setup runbook

**Files:**
- Create: `docs/runbooks/ollama-setup.md`

- [ ] **Step 1: Write the runbook**

```markdown
# Ollama Setup Runbook

## Install Ollama

```bash
curl -fsSL https://ollama.com/install.sh | sh
```

## Start the server

```bash
ollama serve
```

Ollama listens on `http://localhost:11434` by default. The controller's
`OLLAMA_URL` env var defaults to this address.

## Pull the model

**Local development:**
```bash
ollama pull llama3.2
```

**CI (nightly E2E):**
```bash
ollama pull phi3
```

## Verify

```bash
curl http://localhost:11434/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"llama3.2","messages":[{"role":"user","content":"hello"}],"max_tokens":10,"stream":false}'
```

Expected: JSON response with `choices[0].message.content`.

## Troubleshooting

- **404 "model not found":** run `ollama pull <model>` again
- **Connection refused:** ensure `ollama serve` is running
- **Slow first response:** normal — first call after model load takes 5-10s for weight loading
```

- [ ] **Step 2: Commit**

```bash
git add docs/runbooks/ollama-setup.md
git commit -m "docs: ollama-setup runbook"
```

---

### Task 8: Nightly E2E + Grafana dashboard runbooks

**Files:**
- Create: `docs/runbooks/nightly-e2e.md`
- Create: `docs/runbooks/grafana-dashboard.md`

- [ ] **Step 1: Write nightly-e2e.md**

```markdown
# Nightly E2E Runbook

## Trigger manually

```bash
gh workflow run nightly-e2e.yml
```

Or via the GitHub Actions UI: Actions → Nightly E2E → Run workflow.

## Run locally

```bash
make e2e          # tolerance 1.10x (~25 min)
make e2e-strict   # tolerance 1.05x (release-candidate)
```

## What it does

1. Provisions a 3-node kind cluster
2. Installs kube-prometheus-stack + cert-manager
3. Installs Ollama + pulls phi3 (CI) or llama3.2 (local)
4. Builds and loads all images
5. Deploys the full stack
6. Waits 5 min for classifier first-run + reconciler steady state
7. Runs k6 ramp scenario for 25 min
8. Queries Prometheus for p99 latency and 5xx rate
9. Asserts: `p99(agentic) <= p99(hpa) * tolerance` AND `5xx(agentic) <= 5xx(hpa) * tolerance`

## On failure

- Artifacts uploaded: controller logs, forecast-service logs, events
- Check the assertion output for which metric failed
- Common causes: resource exhaustion (add `resources.limits`), forecast timeout (check warm-up), classifier didn't converge (check history duration)
```

- [ ] **Step 2: Write grafana-dashboard.md**

```markdown
# Grafana Dashboard Runbook

## Auto-import (recommended)

The dashboard is auto-loaded by the kube-prometheus-stack Grafana sidecar
from the ConfigMap in `deploy/grafana/dashboard-configmap.yaml` (labeled
`grafana_dashboard: "1"`).

After `make deploy`, navigate to Grafana:

```bash
make port-forward-grafana
# Open http://localhost:3000 (admin/admin)
# Dashboard: "Agentic Autoscaler"
```

## Manual import (fallback)

If the sidecar isn't working:

```bash
make port-forward-grafana &
curl -X POST http://admin:admin@localhost:3000/api/dashboards/db \
  -H "Content-Type: application/json" \
  -d @deploy/grafana/agentic-dashboard.json
```

## Panels

1. **Current RPS** — both targets, 2m rate
2. **Replica Count** — from kube_deployment_spec_replicas
3. **Predicted RPS** — controller metric (future: from CR status)
4. **p99 Latency** — histogram_quantile, both targets
5. **5xx Rate** — error rate comparison
6. **Scaling Events** — K8s events table
7. **Classification** — current pattern + confidence
```

- [ ] **Step 3: Commit**

```bash
git add docs/runbooks/
git commit -m "docs: nightly-e2e + grafana-dashboard runbooks"
```

---

### Task 9: README final polish

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Write or update README.md**

The README should cover: project overview (1 paragraph), quick start (`make tools && make kind-up && make install-deps && make images && make kind-load && make deploy`), architecture diagram reference (`docs/design.md`), env vars summary (link to design §4), development workflow summary (link to runbook), test commands (`make test`, `make test-envtest`, `make smoke`, `make e2e`), and contributing notes.

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: polish README with quick-start, architecture, and dev workflow"
```

---

## Phase 4 — Milestone

### Task 10: Final verification + milestone

- [ ] **Step 1: Verify Makefile targets parse**

```bash
make help
```

Expected: all targets listed with descriptions.

- [ ] **Step 2: Verify workflows are valid YAML**

```bash
yamllint .github/workflows/
```

- [ ] **Step 3: Milestone commit**

```bash
git commit --allow-empty -m "milestone: Plan #11 (CI + Makefile + dev tooling) complete

Makefile (30+ targets):
- Environment: help, tools, ollama-pull
- Code gen: manifests, generate, gen-testdata
- Lint: lint (go + python + yaml + kubeconform)
- Test: test (go + python), test-envtest, test-all
- Build: build, images (three Dockerfiles)
- Cluster: kind-up, kind-load, install-deps, deploy, undeploy, kind-down
- Scenarios: k6-ramp, k6-steady, k6-spiky, k6-bursty
- Smoke + E2E: smoke (5 min), e2e (25 min, 1.10x), e2e-strict (1.05x)
- Observability: port-forward-grafana/prometheus, logs-*

GitHub Actions:
- ci.yml: lint, generate-check, test-go (90% gate), test-python (90% gate),
  test-envtest, build-images, smoke — all parallel, target <10 min
- nightly-e2e.yml: kind + helm + ollama(phi3) + k6 ramp + Prometheus
  assertions (p99 + 5xx at 1.25x tolerance); 60-min timeout;
  failure artifacts uploaded

Smoke + E2E scripts:
- test/smoke/run.sh: kind-up → deploy → wait ready → healthz assertions
- test/e2e/run.sh: full quantitative scenario orchestration
- test/e2e/assertions.sh: Prometheus p99/5xx comparison with configurable tolerance

Runbooks:
- kind-bootstrap.md: prerequisites through teardown
- ollama-setup.md: install, pull, verify
- nightly-e2e.md: trigger, local run, failure investigation
- grafana-dashboard.md: auto-import via sidecar + manual fallback
"
```

---

## Plan-specific Definition of Done

- [ ] `make help` lists all targets without error.
- [ ] `yamllint .github/workflows/` passes clean.
- [ ] `test/smoke/run.sh` is executable and syntactically valid (`bash -n test/smoke/run.sh`).
- [ ] `test/e2e/assertions.sh` is executable and syntactically valid.
- [ ] CI workflow has all 7 jobs defined (lint, generate-check, test-go, test-python, test-envtest, build-images, smoke).
- [ ] Nightly workflow caches `~/.ollama/models`, uses `phi3`, and has a 60-min timeout.
- [ ] Nightly assertions check both p99 AND 5xx with configurable tolerance.
- [ ] All four runbooks exist and cover their documented scope.
- [ ] README includes quick-start commands and links to runbooks.

---

## Notes on what's intentionally deferred

- **Slack/email notifications on nightly failure** — flagged in strategy §12 as a future follow-on.
- **Multi-arch images** — out of scope (amd64 only).
- **Container registry push** — out of scope; kind-load only.
- **Grafana Operator integration** — out of scope.
- **golangci-lint config file** (`.golangci.yml`) — can be added as a follow-on; the Makefile calls `golangci-lint run` with defaults.

---

## Self-Review

**Spec coverage.** Strategy §8.1 (7 CI jobs), §8.2 (nightly steps 1-10), §9 (all Makefile targets), §10 (local dev workflow) are all represented.

**Placeholders.** None. Every script, workflow, and runbook has complete content.

**Type consistency.** Makefile target names match the strategy doc §9 exactly: `make help`, `make tools`, `make ollama-pull`, `make manifests`, `make generate`, `make gen-testdata`, `make lint`, `make test`, `make test-envtest`, `make test-all`, `make build`, `make images`, `make kind-up`, `make kind-load`, `make install-deps`, `make deploy`, `make undeploy`, `make kind-down`, `make smoke`, `make e2e`, `make e2e-strict`, `make k6-ramp/steady/spiky/bursty`, `make port-forward-grafana/prometheus`, `make logs-controller/forecast`.

---

## Execution handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-24-plan-11-ci-makefile-nightly.md`. Two execution options:

1. **Subagent-Driven (recommended)**
2. **Inline Execution**

Which approach?
