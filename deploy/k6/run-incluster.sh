#!/usr/bin/env bash
# -----------------------------------------------------------------------
# deploy/k6/run-incluster.sh — run a k6 scenario as an in-cluster Job.
#
# Why in-cluster: `kubectl port-forward svc/X` does not load-balance.
# It picks one Endpoint at session start and pins all traffic to it for
# the session's lifetime (kubernetes/kubernetes#15180). For the agentic
# vs HPA comparison this is fatal: regardless of how either autoscaler
# scales, only one pod per side ever receives load, so:
#   - replica counts become decorative (the extra pods sit idle);
#   - HPA's per-pod RPS metric averages near zero (1 pod loaded, N-1
#     idle), so the HPA never scales up;
#   - both reconcilers bottleneck on a single pod, making their tail
#     latencies and 5xx rates artificially equal.
#
# Running k6 as a Pod inside the cluster bypasses port-forward entirely.
# The Pod hits ClusterIP-routed Service DNS, kube-proxy iptables rules
# load-balance to a real Endpoint per connection, and every replica
# carries its share of the load.
#
# Usage:
#   deploy/k6/run-incluster.sh <scenario>
#
# Where <scenario> is one of: ramp, steady, spiky, bursty.
#
# Per-scenario tunables (RAMP_UP_DURATION, STEADY_RPS, SPIKE_PEAK_RPS,
# etc.) can be overridden via env vars before invocation. Defaults are
# set below and match those documented in k6/README.md.
# -----------------------------------------------------------------------
set -euo pipefail

SCENARIO="${1:?usage: $0 <ramp|steady|spiky|bursty>}"
case "$SCENARIO" in
  ramp|steady|spiky|bursty) ;;
  *) echo "unknown scenario: $SCENARIO (expected ramp|steady|spiky|bursty)"; exit 2 ;;
esac

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
NS="${K6_NAMESPACE:-demo}"
JOB_NAME="k6-${SCENARIO}"
TIMEOUT="${K6_TIMEOUT:-1800s}"  # 30 min; longer than the longest scenario.

# Tunable defaults — kept in sync with k6/README.md. envsubst doesn't
# understand shell `${VAR:-default}` syntax, so this is the only place
# defaults live; the Job manifest just references `${VAR}`.
export SCENARIO
export RAMP_UP_DURATION="${RAMP_UP_DURATION:-5m}"
export RAMP_HOLD_DURATION="${RAMP_HOLD_DURATION:-15m}"
export RAMP_DOWN_DURATION="${RAMP_DOWN_DURATION:-5m}"
export RAMP_RPS_PEAK="${RAMP_RPS_PEAK:-200}"
export STEADY_RPS="${STEADY_RPS:-100}"
export STEADY_DURATION="${STEADY_DURATION:-10m}"
export SPIKE_BASE_RPS="${SPIKE_BASE_RPS:-50}"
export SPIKE_PEAK_RPS="${SPIKE_PEAK_RPS:-500}"
export SPIKE_INTERVAL="${SPIKE_INTERVAL:-2m}"
export SPIKE_DURATION="${SPIKE_DURATION:-30s}"
export SPIKY_TOTAL_DURATION="${SPIKY_TOTAL_DURATION:-20m}"
export BURST_SIZE="${BURST_SIZE:-50}"
export BURST_MIN_INTERVAL="${BURST_MIN_INTERVAL:-5}"
export BURST_MAX_INTERVAL="${BURST_MAX_INTERVAL:-30}"
export BURSTY_TOTAL_DURATION="${BURSTY_TOTAL_DURATION:-15m}"
export BURSTY_ITERATIONS="${BURSTY_ITERATIONS:-10000}"

# envsubst whitelist: only substitute the variables we explicitly pass
# in. Without the whitelist, envsubst would also clobber the in-pod
# shell script's $TARGET_AGENTIC_URL / $TARGET_HPA_URL references
# (replacing them with empty strings, since they aren't set in this
# wrapper's env — they're set as Pod-level env vars resolved at
# container start). The single-quoted form is deliberate: envsubst's
# whitelist arg expects literal `$VAR` tokens, not their expanded
# values. shellcheck SC2016 is a false positive here.
# shellcheck disable=SC2016
ENVSUBST_VARS='$SCENARIO $RAMP_UP_DURATION $RAMP_HOLD_DURATION $RAMP_DOWN_DURATION $RAMP_RPS_PEAK $STEADY_RPS $STEADY_DURATION $SPIKE_BASE_RPS $SPIKE_PEAK_RPS $SPIKE_INTERVAL $SPIKE_DURATION $SPIKY_TOTAL_DURATION $BURST_SIZE $BURST_MIN_INTERVAL $BURST_MAX_INTERVAL $BURSTY_TOTAL_DURATION $BURSTY_ITERATIONS'

# Re-create the ConfigMap on every run so script changes are picked up
# without a separate sync step. `--dry-run=client -o yaml | apply -f -`
# pattern is the canonical "create or update" idiom.
echo "==> rebuilding k6-scripts ConfigMap from k6/{lib,scenarios}/"
kubectl create configmap k6-scripts \
    --namespace "$NS" \
    --from-file="${ROOT_DIR}/k6/lib/targets.js" \
    --from-file="${ROOT_DIR}/k6/scenarios/ramp.js" \
    --from-file="${ROOT_DIR}/k6/scenarios/steady.js" \
    --from-file="${ROOT_DIR}/k6/scenarios/spiky.js" \
    --from-file="${ROOT_DIR}/k6/scenarios/bursty.js" \
    --dry-run=client -o yaml | kubectl apply -f -

# Delete any prior Job of the same name. Job specs are immutable on
# fields like template.spec.containers, so we can't kubectl-apply over
# an existing Job — we have to recreate.
echo "==> removing any previous Job/${JOB_NAME}"
kubectl delete job "${JOB_NAME}" -n "$NS" --ignore-not-found --wait=true

# Render the Job manifest with the scenario substituted, plus any
# pre-set environment overrides for tunables.
echo "==> applying Job/${JOB_NAME}"
envsubst "$ENVSUBST_VARS" < "${ROOT_DIR}/deploy/k6/job.yaml" | kubectl apply -f -

# Stream the Pod's logs from the moment it starts running. `wait` on
# `condition=Ready` for the Pod (Job conditions don't fire until the
# Pod terminates), then follow logs. Log streaming exits when the
# container terminates.
echo "==> waiting for k6 pod to start"
kubectl wait --for=condition=Ready pod \
    -n "$NS" -l "job-name=${JOB_NAME}" --timeout=120s

echo "==> streaming k6 logs"
kubectl logs -n "$NS" -l "job-name=${JOB_NAME}" -f --tail=-1 || true

# Wait for the Job to finalise. `condition=complete` returns 0 only
# on success; we explicitly check for failure separately so the script
# exits non-zero on a k6 failure.
echo "==> waiting for Job/${JOB_NAME} to finish (timeout ${TIMEOUT})"
if ! kubectl wait --for=condition=complete --timeout="$TIMEOUT" \
        "job/${JOB_NAME}" -n "$NS"; then
    echo "k6 ${SCENARIO} did not complete successfully" >&2
    kubectl describe "job/${JOB_NAME}" -n "$NS" | tail -30
    exit 1
fi

echo "==> k6 ${SCENARIO} done"
