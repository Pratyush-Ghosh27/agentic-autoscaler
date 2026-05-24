#!/usr/bin/env bash
# -----------------------------------------------------------------------
# test/e2e/run.sh — Quantitative comparison E2E.
#
# Spins up a fresh kind cluster, deploys the full stack (agentic +
# HPA-managed target side-by-side), waits for the classifier's first run
# to converge, drives a 25-minute ramp via k6 against both targets in
# parallel, then runs test/e2e/assertions.sh which pulls Prometheus to
# compare the two reconcilers on p99 latency and 5xx rate.
#
# TOLERANCE controls how much slack the agentic side gets relative to the
# HPA side. Common values:
#   1.10  default  — make e2e
#   1.05  strict   — make e2e-strict (release candidate)
#   1.25  CI       — nightly workflow (variance budget for shared runners)
# -----------------------------------------------------------------------
set -euo pipefail

TOLERANCE="${TOLERANCE:-1.10}"
RAMP_DURATION="${RAMP_DURATION:-25m}"
WARMUP_SECONDS="${WARMUP_SECONDS:-300}"
CLUSTER_NAME="${CLUSTER_NAME:-agentic-e2e-$$}"
IMG_TAG="${IMG_TAG:-$(git rev-parse --short HEAD 2>/dev/null || echo dev)}"
SKIP_BUILD="${SKIP_BUILD:-0}"
KEEP_CLUSTER="${KEEP_CLUSTER:-0}"

cleanup() {
    if [[ "${KEEP_CLUSTER}" == "1" ]]; then
        echo "==> KEEP_CLUSTER=1 — leaving ${CLUSTER_NAME} running"
        return
    fi
    echo "==> cleanup: deleting kind cluster ${CLUSTER_NAME}"
    kind delete cluster --name "${CLUSTER_NAME}" 2>/dev/null || true
}
trap cleanup EXIT

step() { printf "\n\033[1;36m== %s ==\033[0m\n" "$*"; }

# -----------------------------------------------------------------------
step "[1/7] Creating kind cluster (${CLUSTER_NAME}, tolerance ${TOLERANCE})"
kind create cluster --config deploy/kind/cluster.yaml --name "${CLUSTER_NAME}" --wait 3m

# -----------------------------------------------------------------------
if [[ "${SKIP_BUILD}" != "1" ]]; then
    step "[2/7] Building images (IMG_TAG=${IMG_TAG})"
    make images IMG_TAG="${IMG_TAG}"
fi
step "[2/7] Loading images into kind"
kind load docker-image "controller:${IMG_TAG}"       --name "${CLUSTER_NAME}"
kind load docker-image "forecast-service:${IMG_TAG}" --name "${CLUSTER_NAME}"
kind load docker-image "target-app:${IMG_TAG}"       --name "${CLUSTER_NAME}"

# -----------------------------------------------------------------------
step "[3/7] Installing cluster dependencies (Helm)"
make install-deps

# -----------------------------------------------------------------------
step "[4/7] Deploying application manifests"
make deploy IMG_TAG="${IMG_TAG}"

# -----------------------------------------------------------------------
step "[5/7] Waiting for steady state"
kubectl wait --for=condition=available deployment \
    -l control-plane=controller-manager -n agentic-system --timeout=180s
kubectl wait --for=condition=available deployment/forecast-service \
    -n agentic-system --timeout=180s
kubectl wait --for=condition=available deployment/app-agentic -n demo --timeout=120s
kubectl wait --for=condition=available deployment/app-hpa     -n demo --timeout=120s

echo "Sleeping ${WARMUP_SECONDS}s for classifier first-run + reconciler warmup..."
sleep "${WARMUP_SECONDS}"

# -----------------------------------------------------------------------
step "[6/7] Running k6 ramp (${RAMP_DURATION}) against both targets"

# Use cluster-internal DNS through k6's --insecure-skip-tls-verify path:
# we resolve the ClusterIP and inject as plain HTTP, since the nodes are
# directly reachable from the runner via kind's docker network.
TARGET_AGENTIC_URL="http://$(kubectl get svc app-agentic -n demo -o jsonpath='{.spec.clusterIP}')"
TARGET_HPA_URL="http://$(kubectl get svc app-hpa     -n demo -o jsonpath='{.spec.clusterIP}')"
export TARGET_AGENTIC_URL TARGET_HPA_URL

echo "  TARGET_AGENTIC_URL=${TARGET_AGENTIC_URL}"
echo "  TARGET_HPA_URL=${TARGET_HPA_URL}"

k6 run k6/scenarios/ramp.js --duration "${RAMP_DURATION}"

# -----------------------------------------------------------------------
step "[7/7] Asserting quantitative results (TOLERANCE=${TOLERANCE})"
TOLERANCE="${TOLERANCE}" bash test/e2e/assertions.sh

echo
echo "=== E2E PASSED ==="
