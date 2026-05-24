#!/usr/bin/env bash
# -----------------------------------------------------------------------
# test/smoke/run.sh — End-to-end smoke test.
#
# Provisions a fresh kind cluster, installs cert-manager and
# kube-prometheus-stack, deploys the full agentic stack plus the HPA-
# managed comparison target, then asserts the basic liveness / readiness
# of every component.
#
# Used by `make smoke` and by the GitHub Actions PR CI workflow.
# Keeps assertions to "everything is reachable and reconciling"; the
# quantitative comparison (p99, 5xx) lives in test/e2e/run.sh and runs
# on the nightly schedule.
# -----------------------------------------------------------------------
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-agentic-smoke-$$}"
IMG_TAG="${IMG_TAG:-$(git rev-parse --short HEAD 2>/dev/null || echo dev)}"
SKIP_BUILD="${SKIP_BUILD:-0}"

cleanup() {
    echo "==> cleanup: deleting kind cluster ${CLUSTER_NAME}"
    kind delete cluster --name "${CLUSTER_NAME}" 2>/dev/null || true
}
trap cleanup EXIT

step() { printf "\n\033[1;36m== %s ==\033[0m\n" "$*"; }

# -----------------------------------------------------------------------
# 1. Cluster
# -----------------------------------------------------------------------
step "[1/6] Creating kind cluster (${CLUSTER_NAME})"
kind create cluster --config deploy/kind/cluster.yaml --name "${CLUSTER_NAME}" --wait 2m

# -----------------------------------------------------------------------
# 2. Images
# -----------------------------------------------------------------------
if [[ "${SKIP_BUILD}" != "1" ]]; then
    step "[2/6] Building images (IMG_TAG=${IMG_TAG})"
    make images IMG_TAG="${IMG_TAG}"
else
    step "[2/6] Skipping image build (SKIP_BUILD=1)"
fi

step "[2/6] Loading images into kind"
kind load docker-image "controller:${IMG_TAG}"        --name "${CLUSTER_NAME}"
kind load docker-image "forecast-service:${IMG_TAG}"  --name "${CLUSTER_NAME}"
kind load docker-image "target-app:${IMG_TAG}"        --name "${CLUSTER_NAME}"

# -----------------------------------------------------------------------
# 3. Dependencies (cert-manager + kube-prometheus-stack)
# -----------------------------------------------------------------------
step "[3/6] Installing cluster dependencies (Helm)"
make install-deps

# -----------------------------------------------------------------------
# 4. Application
# -----------------------------------------------------------------------
step "[4/6] Deploying application manifests"
make deploy IMG_TAG="${IMG_TAG}"

# -----------------------------------------------------------------------
# 5. Wait for rollout
# -----------------------------------------------------------------------
step "[5/6] Waiting for pods to become ready"
kubectl wait --for=condition=available deployment \
    -l control-plane=controller-manager -n agentic-system --timeout=180s
kubectl wait --for=condition=available deployment/forecast-service \
    -n agentic-system --timeout=180s
kubectl wait --for=condition=available deployment/app-agentic \
    -n demo --timeout=120s
kubectl wait --for=condition=available deployment/app-hpa \
    -n demo --timeout=120s

# -----------------------------------------------------------------------
# 6. Assertions
# -----------------------------------------------------------------------
step "[6/6] Smoke assertions"

# CR exists and is being observed (Phase eventually reaches Ready or Conflict
# — both are valid; "Disabled" only when the kill-switch annotation is set).
PHASE=""
for _ in $(seq 1 30); do
    PHASE=$(kubectl get aas app-agentic -n demo -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
    if [[ -n "${PHASE}" ]]; then break; fi
    sleep 2
done
if [[ -z "${PHASE}" ]]; then
    echo "FAIL: AgenticAutoscaler app-agentic has no .status.phase after 60s"
    kubectl describe aas app-agentic -n demo || true
    exit 1
fi
echo "  AgenticAutoscaler phase: ${PHASE}"

# At least one reconcile event of any reason.
EVENTS=$(kubectl get events -n demo \
    --field-selector involvedObject.kind=AgenticAutoscaler \
    --no-headers 2>/dev/null | wc -l | tr -d ' ')
if [[ "${EVENTS}" -lt 1 ]]; then
    echo "FAIL: no events recorded against the AgenticAutoscaler"
    exit 1
fi
echo "  AgenticAutoscaler events: ${EVENTS}"

# target-app /healthz on both replicas via service.
for svc in app-agentic app-hpa; do
    if ! kubectl run smoke-curl-${svc}-$$ --rm -i --restart=Never -n demo \
            --image=curlimages/curl:8.10.1 --quiet -- \
            curl -fsS --max-time 5 "http://${svc}.demo.svc/healthz" \
            >/dev/null 2>&1; then
        echo "FAIL: ${svc} /healthz unreachable"
        exit 1
    fi
    echo "  ${svc} /healthz: OK"
done

echo
echo "=== Smoke PASSED ==="
