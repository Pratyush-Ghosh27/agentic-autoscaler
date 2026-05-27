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
# Default to `latest` so the in-tree manifests (which all reference
# `<image>:latest`) match what we build + load. Override at invocation if
# you need version traceability:  IMG_TAG=v0.1.0 make smoke
IMG_TAG="${IMG_TAG:-latest}"
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
# `make deploy` already waited for the controller-manager rollout +
# serving-cert. Re-confirm here so a manual run that bypassed `deploy`
# still gets the right preconditions, and pick up the data-plane pieces.
kubectl wait --for=condition=available deployment/agentic-autoscaler-controller-manager \
    -n agentic-autoscaler-system --timeout=180s
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

# Smoke is "everything is wired up correctly" — not "data is flowing".
# The controller intentionally withholds .status.phase until it has
# CLASSIFIER_MIN_POINTS=10 range samples (else it emits the
# `metrics_unavailable` Warning); on a freshly-built cluster with zero
# traffic this can take several minutes. Asserting on phase here would
# require us to drive synthetic load, which belongs in the e2e job.
#
# What we *do* assert in smoke:
#   1. The AgenticAutoscaler CR exists and was admitted (== webhook OK).
#   2. The controller has emitted at least one reconcile Event against it
#      (== reconciler is running and observing the CR; the specific
#      Reason field — PascalCase per K8s convention since v2 G22, e.g.
#      `MetricsUnavailable`, `ForecastUnavailable`, `ScaleUp`, `NoChange`
#      — doesn't matter for smoke).
#   3. The target /healthz endpoints are reachable.

# Wait up to 60 s for at least one Event of any reason to land.
EVENTS=0
for _ in $(seq 1 30); do
    EVENTS=$(kubectl get events -n demo \
        --field-selector involvedObject.kind=AgenticAutoscaler \
        --no-headers 2>/dev/null | wc -l | tr -d ' ')
    if [[ "${EVENTS}" -ge 1 ]]; then break; fi
    sleep 2
done
if [[ "${EVENTS}" -lt 1 ]]; then
    echo "FAIL: no Events emitted against the AgenticAutoscaler after 60s"
    kubectl describe aas app-agentic -n demo || true
    kubectl logs -n agentic-autoscaler-system -l control-plane=controller-manager --tail=100 || true
    exit 1
fi
echo "  AgenticAutoscaler Events observed: ${EVENTS}"

# Surface the current phase if the controller has populated it (purely
# informational — its absence is not a smoke failure).
PHASE=$(kubectl get aas app-agentic -n demo -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
echo "  AgenticAutoscaler phase: ${PHASE:-<not-yet-populated>}"

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
