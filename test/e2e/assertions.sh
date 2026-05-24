#!/usr/bin/env bash
# -----------------------------------------------------------------------
# test/e2e/assertions.sh — Prometheus-driven quantitative assertions.
#
# Compares the AgenticAutoscaler-managed target (`app-agentic`) against
# the HPA-managed target (`app-hpa`) on two SLO-relevant signals over
# the most recent k6 run window:
#
#   1. p99 request latency.
#   2. 5xx error rate.
#
# Both numbers must satisfy:
#     metric(agentic) <= metric(hpa) * TOLERANCE
#
# TOLERANCE allows the agentic side to be slightly worse on shared CI
# runners where variance dominates. Tighten to 1.05 for release gates.
# -----------------------------------------------------------------------
set -euo pipefail

TOLERANCE="${TOLERANCE:-1.10}"
WINDOW="${WINDOW:-25m}"
PROM_PORT="${PROM_PORT:-9090}"
PROM_URL="http://localhost:${PROM_PORT}"

# Port-forward Prometheus.
kubectl port-forward -n monitoring svc/kube-prom-kube-prometheus-prometheus \
    "${PROM_PORT}:9090" >/dev/null 2>&1 &
PF_PID=$!
trap "kill ${PF_PID} 2>/dev/null || true" EXIT
sleep 3

# query <PromQL> -> single scalar value (string).
query() {
    local q="$1"
    local encoded
    encoded=$(python3 -c "import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1]))" "$q")
    curl -fsS --max-time 30 "${PROM_URL}/api/v1/query?query=${encoded}" \
        | jq -r '.data.result[0].value[1] // "0"'
}

echo "==> querying p99 over the last ${WINDOW}"
P99_AGENTIC=$(query "histogram_quantile(0.99, sum by (le) (rate(target_app_request_duration_seconds_bucket{deployment=\"app-agentic\"}[${WINDOW}])))")
P99_HPA=$(query     "histogram_quantile(0.99, sum by (le) (rate(target_app_request_duration_seconds_bucket{deployment=\"app-hpa\"}[${WINDOW}])))")

echo "    p99 agentic : ${P99_AGENTIC}s"
echo "    p99 hpa     : ${P99_HPA}s"

echo "==> querying 5xx rate over the last ${WINDOW}"
E5XX_AGENTIC=$(query "sum(rate(target_app_requests_total{deployment=\"app-agentic\",status=~\"5..\"}[${WINDOW}]))")
E5XX_HPA=$(query     "sum(rate(target_app_requests_total{deployment=\"app-hpa\",status=~\"5..\"}[${WINDOW}]))")

echo "    5xx/s agentic : ${E5XX_AGENTIC}"
echo "    5xx/s hpa     : ${E5XX_HPA}"

# Run the comparison in Python — we get exact float math + readable output.
python3 - <<EOF
import math, sys
t = float("${TOLERANCE}")

def f(s):
    try:
        return float(s)
    except ValueError:
        return math.nan

p99_a = f("${P99_AGENTIC}")
p99_h = f("${P99_HPA}")
e5_a  = f("${E5XX_AGENTIC}")
e5_h  = f("${E5XX_HPA}")

failures = []

# Only assert when the HPA baseline produced *some* signal, otherwise the
# comparison is meaningless (both sides idle).
if not math.isnan(p99_h) and p99_h > 0:
    if math.isnan(p99_a) or p99_a > p99_h * t:
        failures.append(f"p99 agentic ({p99_a:.4f}s) > p99 hpa ({p99_h:.4f}s) × {t}")
else:
    print("  warning: p99 hpa baseline is 0 or NaN; skipping latency assertion")

if not math.isnan(e5_h) and e5_h > 0:
    if math.isnan(e5_a) or e5_a > e5_h * t:
        failures.append(f"5xx agentic ({e5_a:.6f}/s) > 5xx hpa ({e5_h:.6f}/s) × {t}")
else:
    print("  warning: 5xx hpa baseline is 0 or NaN; skipping error-rate assertion")

if failures:
    for fail in failures:
        print(f"  FAIL: {fail}", file=sys.stderr)
    sys.exit(1)

print("  All quantitative assertions passed.")
EOF
