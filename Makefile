# ============================================================
#  Agentic Autoscaler — Makefile
# ============================================================
#  Single entry point for build, test, lint, cluster lifecycle,
#  k6 scenarios, smoke and quantitative E2E. Targets are grouped
#  into ##@ sections so `make help` renders a navigable index.
# ============================================================

SHELL       := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

# ----- Versions (pinned) ------------------------------------------------
GO_VERSION              ?= 1.24
PYTHON_VERSION          ?= 3.12
KIND_VERSION            ?= 0.24.0
HELM_VERSION            ?= 3.16.0
K6_VERSION              ?= 0.54.0
KUSTOMIZE_VERSION       ?= v5.4.3
CONTROLLER_TOOLS_VERSION ?= v0.16.4
ENVTEST_VERSION         ?= release-0.19
ENVTEST_K8S_VERSION     ?= 1.31.0
GOLANGCI_LINT_VERSION   ?= v1.62.2
KUBECONFORM_VERSION     ?= v0.6.7
OLLAMA_MODEL            ?= llama3.2
OLLAMA_MODEL_CI         ?= phi3

# ----- Directories ------------------------------------------------------
ROOT_DIR     := $(shell pwd)
CONTROLLER   := $(ROOT_DIR)
FORECAST_SVC := $(ROOT_DIR)/forecast-service
TARGET_APP   := $(ROOT_DIR)/target-app
LOCALBIN     ?= $(ROOT_DIR)/bin

# ----- Image tags -------------------------------------------------------
# Default to `:latest` so the in-tree manifests (which all reference
# `<image>:latest` with imagePullPolicy: IfNotPresent) match the locally
# built and `kind load`-ed images without any per-build kustomize edits.
# For registry-pushed images, override at the make invocation:
#   make images IMG_TAG=v0.1.0 CONTROLLER_IMG=ghcr.io/me/controller:v0.1.0
IMG_TAG          ?= latest
CONTROLLER_IMG   ?= controller:$(IMG_TAG)
FORECAST_IMG     ?= forecast-service:$(IMG_TAG)
TARGET_APP_IMG   ?= target-app:$(IMG_TAG)
# Legacy alias used by config/manager kustomize edits.
IMG              ?= $(CONTROLLER_IMG)

# ----- Tools ------------------------------------------------------------
KUBECTL          ?= kubectl
KUSTOMIZE        ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN   ?= $(LOCALBIN)/controller-gen
ENVTEST          ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT    ?= $(LOCALBIN)/golangci-lint
KUBECONFORM      ?= $(LOCALBIN)/kubeconform
CONTAINER_TOOL   ?= docker
KIND             ?= kind
HELM             ?= helm
K6               ?= k6
OLLAMA           ?= ollama

# CONTROLLER_GEN_PATHS scopes the generator to project source trees; never
# include "./..." here because GOMODCACHE may live inside the workspace
# (.tools/gopath) and the wildcard would walk the cache and fail to resolve
# transitive go.mod entries.
CONTROLLER_GEN_PATHS ?= "./api/...;./internal/..."

# ============================================================
##@ General
# ============================================================

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} \
	  /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 } \
	  /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

.PHONY: all
all: build ## Build all binaries (alias for `build`).

# ============================================================
##@ Environment
# ============================================================

.PHONY: tools
tools: golangci-lint controller-gen envtest kustomize kubeconform ## Install Go-based dev tools (golangci-lint, controller-gen, setup-envtest, kustomize, kubeconform).
	@echo "==> Go tools ready under $(LOCALBIN)/"
	@echo "    For kind/helm/k6/ollama, see docs/runbooks/."

.PHONY: ollama-pull
ollama-pull: ## Pull the local-dev Ollama model.
	$(OLLAMA) pull $(OLLAMA_MODEL)

.PHONY: ollama-pull-ci
ollama-pull-ci: ## Pull the CI Ollama model (smaller).
	$(OLLAMA) pull $(OLLAMA_MODEL_CI)

# ============================================================
##@ Code Generation
# ============================================================

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole, and CRD manifests.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook \
	  paths=$(CONTROLLER_GEN_PATHS) \
	  output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate DeepCopy methods for the API types.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" \
	  paths=$(CONTROLLER_GEN_PATHS)

.PHONY: gen-testdata
gen-testdata: ## Regenerate testdata/*.json from hack/synthetic (deterministic, seed=42).
	go run ./hack/synthetic --output=testdata --seed=42

.PHONY: fmt
fmt: ## Run go fmt on all modules.
	go fmt ./...
	cd $(TARGET_APP) && go fmt ./...

.PHONY: vet
vet: ## Run go vet on all modules.
	go vet ./...
	cd $(TARGET_APP) && go vet ./...

# ============================================================
##@ Lint
# ============================================================

.PHONY: lint
lint: lint-go lint-python lint-yaml ## Run all linters (Go, Python, YAML/kubeconform).

.PHONY: lint-go
lint-go: golangci-lint ## Run golangci-lint on the controller and target-app modules.
	$(GOLANGCI_LINT) run ./...
	cd $(TARGET_APP) && $(GOLANGCI_LINT) run ./...

.PHONY: lint-go-fix
lint-go-fix: golangci-lint ## Run golangci-lint with --fix.
	$(GOLANGCI_LINT) run --fix
	cd $(TARGET_APP) && $(GOLANGCI_LINT) run --fix

.PHONY: lint-python
lint-python: ## Run ruff + mypy on the forecast service.
	cd $(FORECAST_SVC) && ruff check src/ tests/
	cd $(FORECAST_SVC) && mypy src/

.PHONY: lint-yaml
lint-yaml: kubeconform ## Run yamllint + kubeconform on cluster manifests.
	yamllint -d relaxed deploy/
	$(KUBECONFORM) -strict -summary \
	  deploy/manifests/namespace.yaml \
	  deploy/manifests/target-agentic.yaml \
	  deploy/manifests/target-hpa.yaml \
	  deploy/manifests/forecast-service.yaml \
	  deploy/manifests/hpa.yaml

# ============================================================
##@ Test
# ============================================================

.PHONY: test
test: test-go test-python ## Run Tier-1 tests (Go unit + Python unit/integration).

# Tier-1 packages: pure unit tests, no envtest binary dependency.
# Excludes:
#   - internal/controller and internal/webhook — envtest suites that need
#     kube-apiserver/etcd binaries (covered by test-envtest).
#   - ./api/... — kubebuilder-generated deepcopy code with no tests; its
#     0%-covered statements would pull the total below the 80% gate even
#     though the testable code averages >93%. Compilation of the api
#     packages is still verified by `go build ./...` upstream and by the
#     test-envtest suite.
TEST_GO_PKGS = $$(go list ./internal/... | grep -vE '/(controller|webhook)(/|$$)')

.PHONY: test-go
test-go: ## Run Go unit + adapter + classifier + explainer tests with coverage.
	mkdir -p $(LOCALBIN)
	go test $(TEST_GO_PKGS) -count=1 -coverprofile=$(LOCALBIN)/coverage-controller.out
	cd $(TARGET_APP) && go test ./... -count=1 -coverprofile=../bin/coverage-target.out

.PHONY: test-python
test-python: ## Run forecast-service pytest with 90 % coverage gate.
	cd $(FORECAST_SVC) && pytest tests/ -q --cov=src/forecast --cov-report=term-missing --cov-fail-under=90

.PHONY: test-envtest
test-envtest: manifests envtest ## Run envtest-driven controller + webhook integration tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" \
	  go test ./internal/controller/... ./internal/webhook/... -count=1

.PHONY: test-all
test-all: test test-envtest ## Run every test suite (Tier-1 + Tier-2).

.PHONY: test-smoke-validation
test-smoke-validation: ## Run the pure-Go manifest validation suite.
	go test ./test/smoke/... -count=1

# ============================================================
##@ Build
# ============================================================

.PHONY: build
build: build-controller build-target-app ## Build all Go binaries.

.PHONY: build-controller
build-controller: ## Build the controller binary into bin/manager.
	go build -o bin/manager ./cmd/controller/

.PHONY: build-target-app
build-target-app: ## Build the target-app binary into target-app/bin/.
	cd $(TARGET_APP) && go build -o bin/target-app ./cmd/target-app/

.PHONY: build-forecast
build-forecast: ## Build the forecast-service Python wheel.
	cd $(FORECAST_SVC) && python -m build

.PHONY: run
run: ## Run the controller against the current kube-context.
	go run ./cmd/controller/main.go

.PHONY: images
images: image-controller image-forecast image-target-app ## Build all container images.

.PHONY: image-controller
image-controller: ## Build the controller container image.
	$(CONTAINER_TOOL) build -t $(CONTROLLER_IMG) -f Dockerfile $(ROOT_DIR)

.PHONY: image-forecast
image-forecast: ## Build the forecast-service container image.
	$(CONTAINER_TOOL) build -t $(FORECAST_IMG) -f $(FORECAST_SVC)/Dockerfile $(FORECAST_SVC)

.PHONY: image-target-app
image-target-app: ## Build the target-app container image.
	$(CONTAINER_TOOL) build -t $(TARGET_APP_IMG) -f $(TARGET_APP)/Dockerfile $(TARGET_APP)

.PHONY: docker-build
docker-build: image-controller ## Backwards-compat alias for image-controller.

.PHONY: docker-push
docker-push: ## Push the controller image to the registry referenced by IMG.
	$(CONTAINER_TOOL) push $(CONTROLLER_IMG)

.PHONY: build-installer
build-installer: manifests generate kustomize ## Render the consolidated install YAML to dist/install.yaml.
	mkdir -p dist
	cd config/manager && $(KUSTOMIZE) edit set image controller=$(CONTROLLER_IMG)
	$(KUSTOMIZE) build config/default > dist/install.yaml

# ============================================================
##@ Cluster Lifecycle
# ============================================================

.PHONY: kind-up
kind-up: ## Create the kind cluster from deploy/kind/cluster.yaml.
	$(KIND) create cluster --config deploy/kind/cluster.yaml --name agentic

.PHONY: kind-down
kind-down: ## Delete the agentic kind cluster.
	$(KIND) delete cluster --name agentic

.PHONY: kind-load
kind-load: ## Load all built images into the kind cluster.
	$(KIND) load docker-image $(CONTROLLER_IMG) --name agentic
	$(KIND) load docker-image $(FORECAST_IMG)   --name agentic
	$(KIND) load docker-image $(TARGET_APP_IMG) --name agentic

.PHONY: install-deps
install-deps: ## Helm-install kube-prometheus-stack + cert-manager (CI-friendly values).
	$(HELM) repo add prometheus-community https://prometheus-community.github.io/helm-charts || true
	$(HELM) repo add jetstack https://charts.jetstack.io || true
	$(HELM) repo update
	$(HELM) upgrade --install cert-manager jetstack/cert-manager \
	  -n cert-manager --create-namespace \
	  -f deploy/helm/certmanager-values.yaml --wait
	$(HELM) upgrade --install kube-prom prometheus-community/kube-prometheus-stack \
	  -n monitoring --create-namespace \
	  -f deploy/helm/prometheus-values.yaml --wait --timeout 5m

.PHONY: deploy
deploy: ## Apply all application manifests (namespaces, controller, services, HPA, sample CR).
	$(KUBECTL) apply -f deploy/manifests/namespace.yaml
	$(KUBECTL) apply -k config/default
	@echo "==> waiting for cert-manager to issue the serving certificate..."
	$(KUBECTL) wait --for=condition=Ready certificate/agentic-autoscaler-serving-cert \
	    -n agentic-autoscaler-system --timeout=180s
	@echo "==> waiting for controller-manager rollout..."
	$(KUBECTL) wait --for=condition=available deployment/agentic-autoscaler-controller-manager \
	    -n agentic-autoscaler-system --timeout=180s
	$(KUBECTL) apply -f deploy/manifests/forecast-service.yaml
	$(KUBECTL) apply -f deploy/manifests/target-agentic.yaml
	$(KUBECTL) apply -f deploy/manifests/target-hpa.yaml
	$(KUBECTL) apply -f deploy/manifests/hpa.yaml
	$(KUBECTL) apply -k deploy/grafana
	@echo "==> applying sample AgenticAutoscaler CR (requires webhook ready)..."
	$(KUBECTL) apply -f deploy/manifests/agenticautoscaler-sample.yaml

.PHONY: undeploy
undeploy: ## Remove all application manifests.
	-$(KUBECTL) delete -f deploy/manifests/agenticautoscaler-sample.yaml --ignore-not-found
	-$(KUBECTL) delete -k deploy/grafana --ignore-not-found
	-$(KUBECTL) delete -f deploy/manifests/hpa.yaml --ignore-not-found
	-$(KUBECTL) delete -f deploy/manifests/target-hpa.yaml --ignore-not-found
	-$(KUBECTL) delete -f deploy/manifests/target-agentic.yaml --ignore-not-found
	-$(KUBECTL) delete -f deploy/manifests/forecast-service.yaml --ignore-not-found
	-$(KUBECTL) delete -k config/default --ignore-not-found
	-$(KUBECTL) delete -f deploy/manifests/namespace.yaml --ignore-not-found

.PHONY: install
install: manifests kustomize ## Install only the CRDs into the cluster.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall the CRDs from the cluster.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) delete --ignore-not-found -f -

# ============================================================
##@ Scenarios
# ============================================================

.PHONY: k6-ramp
k6-ramp: ## Run the ramp k6 scenario.
	$(K6) run k6/scenarios/ramp.js

.PHONY: k6-steady
k6-steady: ## Run the steady k6 scenario.
	$(K6) run k6/scenarios/steady.js

.PHONY: k6-spiky
k6-spiky: ## Run the spiky k6 scenario.
	$(K6) run k6/scenarios/spiky.js

.PHONY: k6-bursty
k6-bursty: ## Run the bursty k6 scenario.
	$(K6) run k6/scenarios/bursty.js

# ============================================================
##@ Smoke + E2E
# ============================================================

.PHONY: smoke
smoke: ## Full smoke test (kind-up → deploy → assert → kind-down).
	bash test/smoke/run.sh

.PHONY: e2e
e2e: ## Local quantitative E2E (tolerance 1.10x).
	TOLERANCE=1.10 bash test/e2e/run.sh

.PHONY: e2e-strict
e2e-strict: ## Release-candidate E2E (tolerance 1.05x).
	TOLERANCE=1.05 bash test/e2e/run.sh

.PHONY: test-e2e
test-e2e: manifests generate fmt vet ## Run the kubebuilder-scaffolded Go e2e suite (requires a Kind cluster).
	@command -v $(KIND) >/dev/null 2>&1 || { echo "Kind is not installed"; exit 1; }
	@$(KIND) get clusters | grep -q '.' || { echo "No kind cluster running"; exit 1; }
	go test ./test/e2e/ -v -ginkgo.v

# ============================================================
##@ Observability
# ============================================================

.PHONY: port-forward-grafana
port-forward-grafana: ## Port-forward Grafana to localhost:3000 (admin/prom-operator).
	$(KUBECTL) port-forward -n monitoring svc/kube-prom-grafana 3000:80

.PHONY: port-forward-prometheus
port-forward-prometheus: ## Port-forward Prometheus to localhost:9090.
	$(KUBECTL) port-forward -n monitoring svc/kube-prom-kube-prometheus-prometheus 9090:9090

.PHONY: logs-controller
logs-controller: ## Tail controller logs.
	$(KUBECTL) logs -n agentic-autoscaler-system -l control-plane=controller-manager -f

.PHONY: logs-forecast
logs-forecast: ## Tail forecast-service logs.
	$(KUBECTL) logs -n agentic-system -l app=forecast-service -f

.PHONY: logs-target-agentic
logs-target-agentic: ## Tail the agentic-managed target-app logs.
	$(KUBECTL) logs -n demo -l app=app-agentic -f

.PHONY: logs-target-hpa
logs-target-hpa: ## Tail the HPA-managed target-app logs.
	$(KUBECTL) logs -n demo -l app=app-hpa -f

# ============================================================
##@ Tooling (downloaded into ./bin)
# ============================================================

$(LOCALBIN):
	mkdir -p $(LOCALBIN)

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize into ./bin if missing.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen into ./bin if missing.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest into ./bin if missing.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint into ./bin if missing.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

.PHONY: kubeconform
kubeconform: $(KUBECONFORM) ## Download kubeconform into ./bin if missing.
$(KUBECONFORM): $(LOCALBIN)
	$(call go-install-tool,$(KUBECONFORM),github.com/yannh/kubeconform/cmd/kubeconform,$(KUBECONFORM_VERSION))

# go-install-tool installs a Go-based tool at a specific version into
# $(LOCALBIN). The pattern stamps the binary with a version suffix and
# symlinks it, so a stale checkout doesn't keep an old binary around.
#   $1 - target path with name of binary (e.g. ./bin/controller-gen)
#   $2 - module path (e.g. sigs.k8s.io/controller-tools/cmd/controller-gen)
#   $3 - version (e.g. v0.16.4)
define go-install-tool
@[ -f "$(1)-$(3)" ] || { \
  set -e; \
  package=$(2)@$(3) ; \
  echo "Downloading $${package}" ; \
  rm -f $(1) || true ; \
  GOBIN=$(LOCALBIN) go install $${package} ; \
  mv $(1) $(1)-$(3) ; \
} ; \
ln -sf $(1)-$(3) $(1)
endef
