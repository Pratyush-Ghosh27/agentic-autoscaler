// Ramp scenario — linear ramp 0 → PEAK over RAMP_UP_DURATION, hold at PEAK
// for RAMP_HOLD_DURATION, ramp back to 0 over RAMP_DOWN_DURATION.
//
// Tests the controller's response to a slowly building load: the forecast
// model should pick up the trend before the cap kicks in, and cooldowns
// should not prevent the controller from following a smooth trajectory.
//
// Env vars (all optional, defaults shown):
//   RAMP_UP_DURATION    "5m"
//   RAMP_HOLD_DURATION  "15m"
//   RAMP_DOWN_DURATION  "5m"
//   RAMP_RPS_PEAK       "200"

import http from "k6/http";
import { check } from "k6";
import { getTargets, workURL } from "../lib/targets.js";

const targets = getTargets();

const RAMP_UP = __ENV.RAMP_UP_DURATION || "5m";
const HOLD = __ENV.RAMP_HOLD_DURATION || "15m";
const RAMP_DOWN = __ENV.RAMP_DOWN_DURATION || "5m";
const PEAK = parseInt(__ENV.RAMP_RPS_PEAK || "200");

export const options = {
  scenarios: {
    ramp: {
      executor: "ramping-arrival-rate",
      startRate: 0,
      timeUnit: "1s",
      preAllocatedVUs: 50,
      maxVUs: 200,
      stages: [
        { target: PEAK, duration: RAMP_UP },
        { target: PEAK, duration: HOLD },
        { target: 0, duration: RAMP_DOWN },
      ],
    },
  },
  thresholds: {
    // Per-side 5xx thresholds — the agentic-vs-HPA comparison is structurally
    // asymmetric and a single global threshold conflates the two sides'
    // failure modes into one number that the agentic side can't actually
    // recover.
    //
    // AGENTIC SIDE (strict, 0.05). The v2 hot path queries Prometheus
    // directly with a deployment-level sum, scales aggressively against
    // forecast (not just current state), and respects the cap-binding /
    // step-cap precedence rules. With the workflow's 12m steady-state
    // warm-up (HOT_PATH_MIN_POINTS=10 satisfied before k6 starts) the
    // controller reaches maxReplicas=10 within ~3 minutes of k6 start, well
    // before the ramp hits 200 RPS. At 200 RPS / 10 pods = 20 RPS/pod,
    // utilisation is ~29% of the per-pod theoretical ceiling. Clean
    // operation; 5xx should be effectively zero.
    //
    // HPA SIDE (permissive, 0.50). prometheus-adapter under-counts the
    // `http_requests_per_second` custom metric whenever target-app pods
    // approach saturation: the adapter's metricsQuery
    // (deploy/helm/prometheus-adapter-values.yaml) filters on
    // `status=~"2.."` (excludes 503s, which are exactly the
    // "I'm-at-capacity-please-scale-me" signal target-app emits when its
    // concurrency semaphore is full) and uses a `[1m]` rate window against
    // the 30s default scrape interval (only 2 samples; lose one to a slow
    // /metrics scrape and rate() becomes near-zero). HPA-side runs of this
    // scenario observed an averageValue of 4.7 RPS/pod when actual load
    // was ~50 RPS/pod, leaving HPA stuck at 4 pods serving 200 RPS for the
    // entire hold phase. This is a documented prom-adapter weakness that
    // no amount of agentic-controller tuning can fix — and it's the
    // *whole point* of v2's direct-Prometheus query path (the agentic
    // controller routes around exactly this failure mode).
    //
    // The 0.50 HPA-side threshold catches catastrophic regressions (HPA
    // scaling to 0, complete adapter outage) without gating the build on
    // the structural prom-adapter behaviour. See the nightly-e2e failure
    // diagnosis archived in PR conversation for the full evidence.
    "http_req_failed{url:agentic}":    ["rate<0.05"],
    "http_req_failed{url:hpa}":        ["rate<0.50"],
    "http_req_duration{url:agentic}":  ["p(95)<2000"],
    "http_req_duration{url:hpa}":      ["p(95)<2000"],
  },
};

export default function () {
  const resA = http.post(workURL(targets.agentic), null, {
    tags: { url: "agentic" },
  });
  check(resA, {
    "agentic status 2xx or 503": (r) => r.status === 200 || r.status === 503,
  });

  const resH = http.post(workURL(targets.hpa), null, {
    tags: { url: "hpa" },
  });
  check(resH, {
    "hpa status 2xx or 503": (r) => r.status === 200 || r.status === 503,
  });
}
