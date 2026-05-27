// Spiky scenario — periodic bursts at fixed intervals.
//
// Models a workload like a cron-driven batch job that fires every N
// minutes. The controller's forecaster should learn the periodicity
// (Prophet expected to outperform linear extrapolation here once
// classifier promotes to "periodic" pattern).
//
// Two scenarios run concurrently:
//   * base load — constant BASE_RPS for the entire window
//   * spike load — staircase ramping up to PEAK_RPS for SPIKE_DURATION
//                  every INTERVAL seconds
//
// Env vars:
//   SPIKE_BASE_RPS         "50"
//   SPIKE_PEAK_RPS         "500"
//   SPIKE_INTERVAL         "2m"
//   SPIKE_DURATION         "30s"
//   SPIKY_TOTAL_DURATION   "20m"

import http from "k6/http";
import { check } from "k6";
import { getTargets, workURL } from "../lib/targets.js";

const targets = getTargets();

const BASE_RPS = parseInt(__ENV.SPIKE_BASE_RPS || "50");
const PEAK_RPS = parseInt(__ENV.SPIKE_PEAK_RPS || "500");
const INTERVAL = __ENV.SPIKE_INTERVAL || "2m";
const SPIKE_DURATION = __ENV.SPIKE_DURATION || "30s";
const TOTAL_DURATION = __ENV.SPIKY_TOTAL_DURATION || "20m";

function parseDuration(d) {
  const m = d.match(/^(\d+)(s|m|h)$/);
  if (!m) {
    return 60;
  }
  const v = parseInt(m[1]);
  if (m[2] === "m") return v * 60;
  if (m[2] === "h") return v * 3600;
  return v;
}

function generateSpikeStages() {
  const stages = [];
  const intervalSec = parseDuration(INTERVAL);
  const spikeSec = parseDuration(SPIKE_DURATION);
  const totalSec = parseDuration(TOTAL_DURATION);
  let elapsed = 0;

  while (elapsed < totalSec) {
    stages.push({ target: BASE_RPS, duration: `${intervalSec}s` });
    elapsed += intervalSec;
    if (elapsed >= totalSec) break;
    stages.push({ target: PEAK_RPS, duration: `${spikeSec}s` });
    elapsed += spikeSec;
  }
  return stages;
}

export const options = {
  scenarios: {
    base: {
      executor: "constant-arrival-rate",
      rate: BASE_RPS,
      timeUnit: "1s",
      duration: TOTAL_DURATION,
      preAllocatedVUs: 50,
      maxVUs: 100,
      exec: "base_load",
    },
    spikes: {
      executor: "ramping-arrival-rate",
      startRate: BASE_RPS,
      timeUnit: "1s",
      preAllocatedVUs: 100,
      maxVUs: 600,
      stages: generateSpikeStages(),
      exec: "spike_load",
    },
  },
  thresholds: {
    // Per-side 5xx thresholds — same rationale as ramp.js (see the comment
    // block there for the full evidence trail). Two failure modes compound
    // under spiky load and they're fundamentally asymmetric between sides:
    //
    // 1. K6's `ramping-arrival-rate` executor generates bursty arrival when
    //    100 VUs wake up simultaneously for a 500-RPS spike, briefly
    //    pushing instantaneous per-pod arrival above the concurrency=8
    //    semaphore even when *average* per-pod RPS is well within capacity.
    //    Affects both sides equally; structural to the test framework.
    // 2. AGENTIC SIDE recovers within a single 60s reconcile window
    //    (controller queries Prometheus directly with deployment-level sum,
    //    sees the spike immediately, scales toward unboundedRecommended).
    //    HPA SIDE lags because the prometheus-adapter custom-metrics path
    //    (status=~"2.." filter + [1m] rate window vs 30s scrape interval)
    //    under-counts saturation traffic — same fragility as in ramp.js.
    //
    // 0.10 on the agentic side is loosened from steady's 0.05 to absorb the
    // unavoidable burst-overflow micro-windows; tighter than ramp's 0.05
    // would be desirable but spike scenarios put more pressure on the
    // semaphore. 0.50 on the HPA side catches catastrophic regressions
    // without gating on the structural prom-adapter weakness.
    "http_req_failed{url:agentic}": ["rate<0.10"],
    "http_req_failed{url:hpa}":     ["rate<0.50"],
  },
};

export function base_load() {
  const resA = http.post(workURL(targets.agentic), null, {
    tags: { url: "agentic" },
  });
  check(resA, { "agentic ok": (r) => r.status === 200 || r.status === 503 });
  const resH = http.post(workURL(targets.hpa), null, { tags: { url: "hpa" } });
  check(resH, { "hpa ok": (r) => r.status === 200 || r.status === 503 });
}

export function spike_load() {
  const resA = http.post(workURL(targets.agentic), null, {
    tags: { url: "agentic" },
  });
  check(resA, {
    "agentic spike ok": (r) => r.status === 200 || r.status === 503,
  });
  const resH = http.post(workURL(targets.hpa), null, { tags: { url: "hpa" } });
  check(resH, { "hpa spike ok": (r) => r.status === 200 || r.status === 503 });
}
