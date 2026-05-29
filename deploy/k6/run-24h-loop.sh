#!/usr/bin/env bash
# -----------------------------------------------------------------------
# deploy/k6/run-24h-loop.sh — drive ~24h of varied traffic by replaying
# the existing short k6 scenarios back-to-back.
#
# Hackathon-branch addition. Complement to deploy/k6/run-incluster.sh,
# which only runs one scenario at a time. This wrapper sequences a
# round-robin schedule that exercises every traffic shape the agentic
# autoscaler claims to handle (smooth ramp -> steady plateau -> spiky
# periodicity -> bursty noise) and repeats it until DURATION_HOURS
# wall-clock time elapses. Each scenario's tunable defaults come from
# run-incluster.sh; this script only orchestrates.
#
# Why not the diurnal scenario? Two complementary use cases:
#   - diurnal.js (k6-incluster-diurnal) is one *continuous* time series
#     with realistic day-shape — best for showing the classifier's
#     hourly-profile / Prophet hour-baseline regressor engaging on a
#     single uninterrupted history.
#   - This loop is *segmented* — each scenario boundary is a discrete
#     pattern change. Best for showing the classifier flipping between
#     PatternGradualRamp / PatternFlat / PatternPeriodic / PatternSpiky
#     as the data shifts, and for stressing the auto-dispatcher's
#     model-selection logic across a wider variety of shapes.
#
# Usage:
#   deploy/k6/run-24h-loop.sh                  # default: 24h, all 4 scenarios
#   DURATION_HOURS=6 deploy/k6/run-24h-loop.sh # short demo cycle
#   SCENARIOS="ramp steady" deploy/k6/run-24h-loop.sh  # subset only
#   COOLDOWN_SECONDS=120 deploy/k6/run-24h-loop.sh     # longer gap between runs
#
# Env vars:
#   DURATION_HOURS    wall-clock ceiling for the whole loop (default 24)
#   SCENARIOS         space-separated scenario names (default
#                     "ramp steady spiky bursty"); each must be one of
#                     ramp|steady|spiky|bursty (diurnal excluded — use
#                     the dedicated wrapper for that)
#   COOLDOWN_SECONDS  pause between scenario runs (default 60) so the
#                     controller's classifier window sees a clear pattern
#                     boundary rather than overlapping tails
#   K6_NAMESPACE      forwarded to run-incluster.sh (default "demo")
#
# Each scenario invocation honours its own per-scenario tunables via the
# same env vars consumed by run-incluster.sh — pre-set RAMP_RPS_PEAK,
# STEADY_DURATION, etc. before invoking this script if you want
# non-default shapes.
#
# Exit codes:
#   0 — DURATION_HOURS elapsed cleanly (no scenario failure)
#   1 — at least one scenario invocation failed (loop aborts immediately)
#   130 — SIGINT/SIGTERM (the trapped child also tears down the running
#         Job; see run-incluster.sh)
# -----------------------------------------------------------------------
set -euo pipefail

DURATION_HOURS="${DURATION_HOURS:-24}"
SCENARIOS_LIST="${SCENARIOS:-ramp steady spiky bursty}"
COOLDOWN_SECONDS="${COOLDOWN_SECONDS:-60}"

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
RUNNER="${ROOT_DIR}/deploy/k6/run-incluster.sh"

if [ ! -x "$RUNNER" ]; then
    echo "missing or non-executable runner: $RUNNER" >&2
    exit 2
fi

# Validate every requested scenario up front so a typo doesn't fail 6h
# into the loop. The matching list is intentionally hand-maintained
# (rather than parsed out of run-incluster.sh) — diurnal is excluded
# because its own ceiling is hours-long; replaying it inside a loop is
# almost never what the user wants.
for s in $SCENARIOS_LIST; do
    case "$s" in
        ramp|steady|spiky|bursty) ;;
        *) echo "unknown scenario in SCENARIOS: '$s' (expected ramp|steady|spiky|bursty)" >&2; exit 2 ;;
    esac
done

START_TS="$(date +%s)"
# awk for arithmetic so DURATION_HOURS can be a float (e.g. 0.5 for a
# 30-minute smoke test).
DEADLINE_TS="$(awk "BEGIN { printf \"%d\", ${START_TS} + ${DURATION_HOURS} * 3600 }")"

echo "==> k6 24h loop starting"
echo "    duration : ${DURATION_HOURS}h (deadline: $(date -d "@${DEADLINE_TS}" 2>/dev/null || date -r "${DEADLINE_TS}" 2>/dev/null || echo "+${DURATION_HOURS}h"))"
echo "    cycle    : ${SCENARIOS_LIST}"
echo "    cooldown : ${COOLDOWN_SECONDS}s between scenarios"

# Round-robin index — survives across the while loop so successive
# cycles continue advancing. `read -ra` is the shellcheck-clean way to
# split a space-separated string into an array; word-splitting is
# exactly what we want here, so SC2206 is a false-positive guard.
read -ra SCENARIOS_ARR <<<"$SCENARIOS_LIST"
N_SCENARIOS=${#SCENARIOS_ARR[@]}
i=0
cycle=1

cleanup_on_signal() {
    echo
    echo "==> 24h loop caught signal, exiting (current child Job — if any — is torn down by its own trap)"
    exit 130
}
trap cleanup_on_signal INT TERM

while [ "$(date +%s)" -lt "$DEADLINE_TS" ]; do
    SCENARIO="${SCENARIOS_ARR[$((i % N_SCENARIOS))]}"
    NOW_TS="$(date +%s)"
    REMAINING_S=$(( DEADLINE_TS - NOW_TS ))
    REMAINING_H="$(awk "BEGIN { printf \"%.2f\", ${REMAINING_S} / 3600.0 }")"
    echo
    echo "===================================================================="
    echo "==> cycle #${cycle} step $((i % N_SCENARIOS + 1))/${N_SCENARIOS}: ${SCENARIO}"
    echo "==> elapsed: $(( (NOW_TS - START_TS) / 60 ))m  remaining: ${REMAINING_H}h"
    echo "===================================================================="

    # Run the scenario. We deliberately *don't* set K6_TIMEOUT here —
    # each scenario's own k6 stages decide its duration, and the
    # wrapper's default 1h ceiling is plenty for all non-diurnal
    # scenarios.
    if ! bash "$RUNNER" "$SCENARIO"; then
        echo "==> scenario ${SCENARIO} failed (cycle #${cycle}); aborting 24h loop" >&2
        exit 1
    fi

    i=$((i + 1))
    if [ "$((i % N_SCENARIOS))" -eq 0 ]; then
        cycle=$((cycle + 1))
    fi

    # Short cooldown between runs. Without it, the previous scenario's
    # in-flight requests and the next scenario's startup overlap in the
    # controller's classifier window, blurring the pattern boundary
    # the loop is specifically meant to expose.
    NOW_TS="$(date +%s)"
    if [ "$NOW_TS" -ge "$DEADLINE_TS" ]; then
        break
    fi
    echo "==> cooldown ${COOLDOWN_SECONDS}s before next scenario"
    sleep "$COOLDOWN_SECONDS"
done

ELAPSED_S=$(( $(date +%s) - START_TS ))
echo
echo "==> 24h loop done after $(( ELAPSED_S / 60 ))m ($(( ELAPSED_S / 3600 ))h $(( (ELAPSED_S % 3600) / 60 ))m), ${cycle} cycle(s) of ${SCENARIOS_LIST}"
