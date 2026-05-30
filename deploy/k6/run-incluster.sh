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

SCENARIO="${1:?usage: $0 <ramp|steady|spiky|bursty|diurnal|rotating|schedule>}"
case "$SCENARIO" in
  ramp|steady|spiky|bursty|diurnal|rotating|schedule) ;;
  *) echo "unknown scenario: $SCENARIO (expected ramp|steady|spiky|bursty|diurnal|rotating|schedule)"; exit 2 ;;
esac

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
NS="${K6_NAMESPACE:-demo}"
JOB_NAME="k6-${SCENARIO}"

# Long-running scenarios — the ones that take >1h and where accidental
# wrapper termination is the most common reason a 23h run "disappears".
# For these we default to NOT trapping signals so a Ctrl-C, terminal
# close (SIGHUP), or ssh disconnect leaves the in-cluster Job running
# untouched. Short scenarios keep the original trap-and-delete
# behaviour because their failure mode is "user changed their mind
# mid-experiment and wants the load to stop immediately".
#
# Override either direction with K6_NO_TRAP=1 (force no-trap) or
# K6_NO_TRAP=0 (force trap).
case "$SCENARIO" in
  diurnal|rotating|schedule) DEFAULT_NO_TRAP=1 ;;
  *)                         DEFAULT_NO_TRAP=0 ;;
esac
NO_TRAP="${K6_NO_TRAP:-${DEFAULT_NO_TRAP}}"

# Trap SIGINT/SIGTERM so Ctrl-C from a developer streaming logs does the
# obvious thing — tear down the in-cluster Job — instead of leaving it
# running. Without this, an interrupted `make k6-incluster-ramp` exits
# the wrapper but the Job keeps producing load against `app-agentic` and
# `app-hpa`, which silently confounds the next experiment (the HPA twin
# keeps scaling against the orphaned load). The trap is best-effort: if
# the Job hasn't been applied yet (e.g. Ctrl-C during the ConfigMap
# rebuild) the delete is a no-op via `--ignore-not-found`.
cleanup_on_signal() {
    echo "==> caught signal, deleting Job/${JOB_NAME} in ${NS}"
    kubectl delete job "${JOB_NAME}" -n "$NS" --ignore-not-found --wait=false || true
    exit 130
}
detach_on_signal() {
    # Long-run mode: exit the wrapper but leave the Job running.
    # User can re-attach with `kubectl logs -f -n demo job/${JOB_NAME}`
    # or monitor with `kubectl get job ${JOB_NAME} -n ${NS} -w`.
    echo ""
    echo "==> wrapper detaching (signal received); Job/${JOB_NAME} continues running"
    echo "    re-attach logs:  kubectl logs -f -n ${NS} job/${JOB_NAME}"
    echo "    monitor status:  kubectl get job ${JOB_NAME} -n ${NS} -w"
    echo "    stop the run:    kubectl delete job ${JOB_NAME} -n ${NS}"
    exit 130
}
if [ "$NO_TRAP" = "1" ]; then
    trap detach_on_signal INT TERM HUP
else
    trap cleanup_on_signal INT TERM
fi
# 60 min covers the longest currently-defined scenario (sustained =
# 5m up + 30m hold + 5m down = 40m of k6, plus Pod schedule + image
# pull + finalisation overhead). The previous 30m default would silently
# time out on the sustained scenario before the Job actually completed —
# the Job kept running, but the wrapper exited 1 and CI marked the run
# failed. `kubectl wait` returns as soon as the Job condition transitions
# to complete, so this is a ceiling not a floor; shorter scenarios still
# finish in their own k6-script-driven duration.
# Computed *after* the per-scenario tunables below are exported, so the
# diurnal scenario can derive its ceiling from DIURNAL_TOTAL_HOURS.

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
# Diurnal scenario tunables (hackathon-branch addition).
export DIURNAL_BASE_RPS="${DIURNAL_BASE_RPS:-20}"
export DIURNAL_PEAK_RPS="${DIURNAL_PEAK_RPS:-300}"
export DIURNAL_SPIKE_RPS="${DIURNAL_SPIKE_RPS:-500}"
export DIURNAL_TOTAL_HOURS="${DIURNAL_TOTAL_HOURS:-24}"
# Rotating scenario tunables (hackathon-two-branch addition).
export ROTATING_CYCLES="${ROTATING_CYCLES:-10}"
export ROTATING_STEADY_RPS="${ROTATING_STEADY_RPS:-100}"
export ROTATING_RAMP_PEAK_RPS="${ROTATING_RAMP_PEAK_RPS:-200}"
export ROTATING_SPIKE_RPS="${ROTATING_SPIKE_RPS:-200}"
export ROTATING_BURSTY_FLOOR="${ROTATING_BURSTY_FLOOR:-60}"
export ROTATING_BURSTY_CEILING="${ROTATING_BURSTY_CEILING:-140}"
# Schedule scenario tunables (hackathon-five-branch addition).
export SCHEDULE_DAYS="${SCHEDULE_DAYS:-2}"
export SCHEDULE_LOW_RPS="${SCHEDULE_LOW_RPS:-100}"
export SCHEDULE_MEDLO_RPS="${SCHEDULE_MEDLO_RPS:-150}"
export SCHEDULE_MED_RPS="${SCHEDULE_MED_RPS:-200}"
export SCHEDULE_SPIKE_RPS="${SCHEDULE_SPIKE_RPS:-350}"
export SCHEDULE_TRANSITION_S="${SCHEDULE_TRANSITION_S:-30}"

# Per-scenario default timeout. Long-running scenarios derive their
# ceiling from the relevant tunable (+1h slack for image pull, Pod
# schedule, k6 startup, ConfigMap reload, and the polling interval).
# All other scenarios keep the legacy 1h ceiling.
case "$SCENARIO" in
  diurnal)
      DEFAULT_TIMEOUT_S="$(awk "BEGIN { printf \"%d\", ${DIURNAL_TOTAL_HOURS} * 3600 + 3600 }")"
      TIMEOUT="${K6_TIMEOUT:-${DEFAULT_TIMEOUT_S}s}"
      ;;
  rotating)
      # ROTATING_CYCLES * 140 min per cycle * 60 s/min + 1h slack.
      DEFAULT_TIMEOUT_S="$(awk "BEGIN { printf \"%d\", ${ROTATING_CYCLES} * 140 * 60 + 3600 }")"
      TIMEOUT="${K6_TIMEOUT:-${DEFAULT_TIMEOUT_S}s}"
      ;;
  schedule)
      # SCHEDULE_DAYS * 24h * 3600s/h + 1h slack. Default 2 days = 49h.
      DEFAULT_TIMEOUT_S="$(awk "BEGIN { printf \"%d\", ${SCHEDULE_DAYS} * 24 * 3600 + 3600 }")"
      TIMEOUT="${K6_TIMEOUT:-${DEFAULT_TIMEOUT_S}s}"
      ;;
  *)
      TIMEOUT="${K6_TIMEOUT:-3600s}"
      ;;
esac

# envsubst whitelist: only substitute the variables we explicitly pass
# in. Without the whitelist, envsubst would also clobber the in-pod
# shell script's $TARGET_AGENTIC_URL / $TARGET_HPA_URL references
# (replacing them with empty strings, since they aren't set in this
# wrapper's env — they're set as Pod-level env vars resolved at
# container start). The single-quoted form is deliberate: envsubst's
# whitelist arg expects literal `$VAR` tokens, not their expanded
# values. shellcheck SC2016 is a false positive here.
# shellcheck disable=SC2016
ENVSUBST_VARS='$SCENARIO $RAMP_UP_DURATION $RAMP_HOLD_DURATION $RAMP_DOWN_DURATION $RAMP_RPS_PEAK $STEADY_RPS $STEADY_DURATION $SPIKE_BASE_RPS $SPIKE_PEAK_RPS $SPIKE_INTERVAL $SPIKE_DURATION $SPIKY_TOTAL_DURATION $BURST_SIZE $BURST_MIN_INTERVAL $BURST_MAX_INTERVAL $BURSTY_TOTAL_DURATION $BURSTY_ITERATIONS $DIURNAL_BASE_RPS $DIURNAL_PEAK_RPS $DIURNAL_SPIKE_RPS $DIURNAL_TOTAL_HOURS $ROTATING_CYCLES $ROTATING_STEADY_RPS $ROTATING_RAMP_PEAK_RPS $ROTATING_SPIKE_RPS $ROTATING_BURSTY_FLOOR $ROTATING_BURSTY_CEILING $SCHEDULE_DAYS $SCHEDULE_LOW_RPS $SCHEDULE_MEDLO_RPS $SCHEDULE_MED_RPS $SCHEDULE_SPIKE_RPS $SCHEDULE_TRANSITION_S'

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
    --from-file="${ROOT_DIR}/k6/scenarios/diurnal.js" \
    --from-file="${ROOT_DIR}/k6/scenarios/rotating.js" \
    --from-file="${ROOT_DIR}/k6/scenarios/schedule.js" \
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

# For long-running scenarios, surface the survive-disconnect contract
# loudly. The most common cause of "the 23h run disappeared overnight"
# is the user starting the wrapper in an interactive shell, then
# closing the terminal / suspending the laptop, which SIGHUPs the
# wrapper. With NO_TRAP=1 (the long-scenario default) the Job survives
# this, but only if the user knows it does — otherwise they'll
# `kubectl delete` defensively. Print the resume instructions before
# the Pod even starts so they can be captured by `script` or screenshot.
case "$SCENARIO" in
  diurnal|rotating|schedule)
    cat <<EOF
==> LONG-RUNNING SCENARIO (${SCENARIO}, expected duration > 1h)

This wrapper will NOT delete the in-cluster Job if interrupted (Ctrl-C,
terminal close, or SSH disconnect). The Job keeps running until k6
finishes, fails, or you explicitly delete it.

Recommended: launch this command under tmux or nohup so the polling
loop survives terminal close:

    tmux new -s k6 "make k6-incluster-${SCENARIO}"
    # OR
    nohup make k6-incluster-${SCENARIO} > k6-${SCENARIO}.log 2>&1 &

If you forget and Ctrl-C / disconnect anyway, the Job is still running.
Re-attach with:

    kubectl logs -f -n ${NS} job/${JOB_NAME}
    kubectl get job ${JOB_NAME} -n ${NS} -w
    kubectl describe job ${JOB_NAME} -n ${NS}

To override and force trap-and-delete behaviour: K6_NO_TRAP=0 make ...

EOF
    ;;
esac

# Stream the Pod's logs from the moment it starts running. `wait` on
# `condition=Ready` for the Pod (Job conditions don't fire until the
# Pod terminates), then follow logs. Log streaming exits when the
# container terminates.
#
# 300s wait covers cold image pulls on slow connections (grafana/k6
# is ~140MB compressed) — the previous 120s ceiling failed on a fresh
# kind cluster the first time each day, even though the Pod itself
# would have come up healthy ~30s later.
echo "==> waiting for k6 pod to start"
kubectl wait --for=condition=Ready pod \
    -n "$NS" -l "job-name=${JOB_NAME}" --timeout=300s

echo "==> streaming k6 logs"
kubectl logs -n "$NS" -l "job-name=${JOB_NAME}" -f --tail=-1 || true

# Poll for either Complete or Failed. We deliberately do NOT use a
# single `kubectl wait --for=condition=complete --timeout=$TIMEOUT`
# call: when k6 exits non-zero (e.g., ramp.js trips the
# `http_req_failed: rate<0.05` threshold) the Job transitions to
# condition=Failed and condition=Complete never becomes True, so the
# wait blocks for the full TIMEOUT. Combined with the workflow's
# `timeout-minutes`, the runner gets cancelled before either the
# failure-artifact step or the next k6 scenario can run — which is
# exactly the failure mode the 7 cancelled nightly runs surfaced.
#
# Polling `.status.{succeeded,failed}` (incremented by the Job
# controller; `backoffLimit: 0` in deploy/k6/job.yaml means the first
# Pod failure flips status.failed=1 immediately) lets us short-circuit
# on either terminal state in seconds, so:
#   - a passing k6 returns within one poll interval of Pod termination;
#   - a failing k6 returns the same way and propagates exit 1, which
#     trips the workflow's `if: failure()` artifact-collection step;
#   - a genuinely hung Pod still hits the TIMEOUT ceiling and exits 1,
#     identical to the previous behaviour.
echo "==> waiting for Job/${JOB_NAME} to finish (timeout ${TIMEOUT})"
# TIMEOUT is a Go-style duration but every in-tree caller uses the
# `<seconds>s` form. Strip the trailing `s` so we can do arithmetic;
# any non-numeric remainder will fail loudly via the `(( ... ))`
# evaluation below rather than silently misbehave.
TIMEOUT_SECONDS="${TIMEOUT%s}"
DEADLINE=$(( $(date +%s) + TIMEOUT_SECONDS ))
# Poll interval: 5s is fine for the 1h scenarios (~720 API calls), but
# the 24h scenarios would generate ~17,000 API calls — enough to trip
# kube-apiserver client-side rate limiting on a small kind cluster and
# delay the actual job-status read. 30s for long scenarios cuts that
# to ~2,800 calls with no meaningful loss of detection latency (k6 is
# emitting metrics every second; an extra 25s to notice the Job
# finished doesn't matter on a 23h run).
case "$SCENARIO" in
  diurnal|rotating|schedule) POLL_INTERVAL=30 ;;
  *)                         POLL_INTERVAL=5  ;;
esac
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
    SUCCEEDED="$(kubectl get "job/${JOB_NAME}" -n "$NS" \
        -o jsonpath='{.status.succeeded}' 2>/dev/null || echo "")"
    FAILED="$(kubectl get "job/${JOB_NAME}" -n "$NS" \
        -o jsonpath='{.status.failed}' 2>/dev/null || echo "")"
    if [ "$SUCCEEDED" = "1" ]; then
        echo "==> k6 ${SCENARIO} done"
        exit 0
    fi
    if [ "$FAILED" = "1" ]; then
        echo "k6 ${SCENARIO} failed (Job/${JOB_NAME} .status.failed=1)" >&2
        kubectl describe "job/${JOB_NAME}" -n "$NS" | tail -30
        # Post-mortem helper: print the Pod's last 100 log lines if
        # the Pod is still around (ttlSecondsAfterFinished=86400 in
        # the Job template keeps it alive for 24h, so this almost
        # always succeeds for hackathon-four onwards).
        echo ""
        echo "==> Pod logs (last 100 lines):"
        kubectl logs -n "$NS" -l "job-name=${JOB_NAME}" --tail=100 || true
        exit 1
    fi
    sleep "$POLL_INTERVAL"
done
echo "k6 ${SCENARIO} did not reach a terminal state within ${TIMEOUT}" >&2
kubectl describe "job/${JOB_NAME}" -n "$NS" | tail -30
exit 1
