#!/usr/bin/env bash
# -----------------------------------------------------------------------
# test/e2e/assertions-gbdt.sh — Prometheus-driven nightly assertion that
# the gbdt_quantile forecaster path was actually exercised end-to-end.
#
# Context (Plan 18 / docs/v2-acceptance-coverage.md row 9):
#   The classifier auto-selects gbdt_quantile only after enough on-CR
#   history has accumulated to fingerprint a "spiky" pattern, which
#   takes longer than a single nightly run can afford. To get a
#   deterministic gbdt_quantile path on the nightly clock, the
#   nightly-e2e workflow patches the AgenticAutoscaler CR with
#   `spec.preferredForecaster: gbdt_quantile` *before* running the
#   spiky k6 scenario. Every reconcile during that scenario therefore
#   calls the forecast service with `preferred_model="gbdt_quantile"`,
#   which (per dispatch.recommend) increments
#   `forecast_dispatch_total{model_used="gbdt_quantile"}`.
#
# Assertion: at the end of the spiky scenario, the resolved-after-
# fallback counter for gbdt_quantile must be > 0. This is the strongest
# possible "the gbdt path actually served traffic" signal — it doesn't
# depend on internal logging, doesn't race the classifier's warm-up,
# and treats any silent fallback to linear_extrap as a failure (because
# the gbdt counter would stay at 0).
#
# A NaN result here means the metric series doesn't exist at all — most
# likely the forecast service didn't get scraped, the metric isn't
# registered, or the spiky scenario never reached the forecast service.
# Each of those is a real regression, not a tolerance issue, so we fail
# loudly with the underlying query result for the on-call to triage.
# -----------------------------------------------------------------------
set -euo pipefail

PROM_PORT="${PROM_PORT:-9090}"
PROM_URL="http://localhost:${PROM_PORT}"

# Port-forward Prometheus.
kubectl port-forward -n monitoring svc/kube-prom-kube-prometheus-prometheus \
    "${PROM_PORT}:9090" >/dev/null 2>&1 &
PF_PID=$!
trap "kill ${PF_PID} 2>/dev/null || true" EXIT
sleep 3

query() {
    local q="$1"
    local encoded
    encoded=$(python3 -c "import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1]))" "$q")
    curl -fsS --max-time 30 "${PROM_URL}/api/v1/query?query=${encoded}" \
        | jq -r '.data.result[0].value[1] // "0"'
}

echo "==> querying forecast_dispatch_total{model_used=\"gbdt_quantile\"}"
GBDT_COUNT=$(query 'forecast_dispatch_total{model_used="gbdt_quantile"}')
echo "    gbdt_quantile dispatches : ${GBDT_COUNT}"

# Diagnostic context: also report linear_extrap and prophet so an on-call
# reading the failure log immediately sees whether the forecast service
# was being hit at all (one of these will be > 0 in any realistic run)
# vs. a wholesale scrape failure.
LINEAR_COUNT=$(query 'forecast_dispatch_total{model_used="linear_extrap"}')
PROPHET_COUNT=$(query 'forecast_dispatch_total{model_used="prophet"}')
echo "    linear_extrap dispatches : ${LINEAR_COUNT}  (diagnostic)"
echo "    prophet dispatches       : ${PROPHET_COUNT}  (diagnostic)"

python3 - <<EOF
import math, sys

def f(s):
    try:
        return float(s)
    except ValueError:
        return math.nan

g = f("${GBDT_COUNT}")
linear = f("${LINEAR_COUNT}")
prophet = f("${PROPHET_COUNT}")

failures = []

if math.isnan(g):
    failures.append(
        "forecast_dispatch_total{model_used=\"gbdt_quantile\"} is NaN — "
        "the metric series doesn't exist (forecast service not scraped, "
        "metric not registered, or spiky run never reached /recommend)"
    )
elif g <= 0:
    diag = f"linear_extrap={linear}, prophet={prophet}"
    if linear > 0 or prophet > 0:
        # Forecast service IS being scraped, but no gbdt dispatches were recorded.
        # Either the CR patch didn't take, the controller isn't honouring
        # preferredForecaster, or every gbdt call fell back to linear_extrap.
        failures.append(
            f"gbdt_quantile dispatch count is 0 despite the spiky scenario "
            f"running with preferredForecaster=gbdt_quantile; "
            f"other forecasters DID serve traffic ({diag}). Most likely "
            f"causes: kubectl patch didn't take, the controller isn't "
            f"forwarding preferredForecaster to /recommend, or gbdt_model "
            f"is silently raising on every call (check forecast logs for "
            f"'gbdt_quantile failed, falling back to linear_extrap')."
        )
    else:
        # Nothing is being scraped — broader failure than just gbdt.
        failures.append(
            f"gbdt_quantile dispatch count is 0 AND no other forecaster "
            f"recorded any dispatches ({diag}) — forecast service is not "
            f"being scraped at all. Check ServiceMonitor and Prometheus "
            f"target health."
        )

if failures:
    for fail in failures:
        print(f"  FAIL: {fail}", file=sys.stderr)
    sys.exit(1)

print(f"  gbdt_quantile path verified: {g:.0f} dispatches recorded.")
EOF
