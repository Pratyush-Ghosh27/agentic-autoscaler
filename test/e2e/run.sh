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
# Default to `latest`; see comment in test/smoke/run.sh for rationale.
IMG_TAG="${IMG_TAG:-latest}"
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
kubectl wait --for=condition=available deployment/agentic-autoscaler-controller-manager \
    -n agentic-autoscaler-system --timeout=180s
kubectl wait --for=condition=available deployment/forecast-service \
    -n agentic-system --timeout=180s
kubectl wait --for=condition=available deployment/app-agentic -n demo --timeout=120s
kubectl wait --for=condition=available deployment/app-hpa     -n demo --timeout=120s

echo "Sleeping ${WARMUP_SECONDS}s for classifier first-run + reconciler warmup..."
sleep "${WARMUP_SECONDS}"

# -----------------------------------------------------------------------
step "[6/7] Running k6 ramp as an in-cluster Job"

# Two reasons we run k6 inside the cluster instead of from the host:
#
#   1. ClusterIPs are not routable from the host on every Docker
#      flavour. Linux Docker shares the kernel routing table with the
#      kind nodes' docker network and *can* reach 10.96.0.0/12; Docker
#      Desktop (macOS, Windows) and rootless Docker can't. The earlier
#      version of this script assumed Linux Docker and broke silently
#      on Desktop. Running k6 in-cluster sidesteps the routing question
#      entirely.
#
#   2. Even on Linux Docker where ClusterIPs are reachable from the
#      host, the previous version that fell through to
#      `kubectl port-forward svc/...` would have been worse: port-forward
#      pins all traffic to a single Endpoint for the session
#      (kubernetes/kubernetes#15180), so any replica > 1 receives no
#      load and the autoscaler comparison becomes meaningless.
#
# Tunables come from the workflow's RAMP_*_DURATION inputs, which the
# in-cluster Job reads as env vars on the Pod. Defaults match k6/README.md.
export RAMP_UP_DURATION="${RAMP_UP_DURATION:-5m}"
export RAMP_HOLD_DURATION="${RAMP_HOLD_DURATION:-15m}"
export RAMP_DOWN_DURATION="${RAMP_DOWN_DURATION:-5m}"

# RAMP_DURATION is the total wall clock the caller asked for. We do
# not pass `--duration` to `k6 run` because in k6 ≥ 0.50 a CLI
# `--duration` *replaces* the script's `scenarios:` block with a
# default 1-VU scenario, silently turning a 200-RPS arrival-rate ramp
# into a single-VU sequential loop. The script's own stages drive the
# duration; RAMP_DURATION is informational here for the operator.
echo "  RAMP_DURATION (informational): ${RAMP_DURATION}"
echo "  RAMP_UP_DURATION:   ${RAMP_UP_DURATION}"
echo "  RAMP_HOLD_DURATION: ${RAMP_HOLD_DURATION}"
echo "  RAMP_DOWN_DURATION: ${RAMP_DOWN_DURATION}"

bash deploy/k6/run-incluster.sh ramp

# -----------------------------------------------------------------------
step "[7/7] Asserting quantitative results (TOLERANCE=${TOLERANCE})"
TOLERANCE="${TOLERANCE}" bash test/e2e/assertions.sh

echo
echo "=== E2E PASSED ==="
