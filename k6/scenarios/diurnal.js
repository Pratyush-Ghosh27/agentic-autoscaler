// Diurnal scenario — synthetic "day-in-the-life" load pattern.
//
// Hackathon-branch addition. Models a realistic production workload:
// overnight trough, morning ramp, midday peak with a lunch spike,
// afternoon plateau with a second spike, evening decline. One k6 Job
// covers the full duration so the controller sees a continuous time
// series — long enough for the classifier to engage
// (CLASSIFIER_MIN_POINTS=22 on hackathon branch) and, at >=12h, for
// HourlyProfileValid=true to unlock Prophet's hour_baseline regressor.
//
// The 24-stage shape below is compressed or expanded to fit
// DIURNAL_TOTAL_HOURS — useful for shorter demo runs that preserve
// the full diurnal shape rather than truncating it to the first N
// hours of trough.
//
// Env vars (all optional, defaults shown):
//   DIURNAL_BASE_RPS       "20"      overnight floor
//   DIURNAL_PEAK_RPS       "300"     midday plateau
//   DIURNAL_SPIKE_RPS      "500"     transient lunch/afternoon spike
//   DIURNAL_TOTAL_HOURS    "24"      total scenario duration (float OK)
//
// Examples:
//   make k6-incluster-diurnal                              # 24h full cycle
//   DIURNAL_TOTAL_HOURS=6  make k6-incluster-diurnal       # 6h compressed
//   DIURNAL_TOTAL_HOURS=0.5 DIURNAL_PEAK_RPS=150 \
//     make k6-incluster-diurnal                            # 30min smoke

import http from "k6/http";
import { check } from "k6";
import { getTargets, workURL } from "../lib/targets.js";

const targets = getTargets();

const BASE  = parseInt(__ENV.DIURNAL_BASE_RPS  || "20");
const PEAK  = parseInt(__ENV.DIURNAL_PEAK_RPS  || "300");
const SPIKE = parseInt(__ENV.DIURNAL_SPIKE_RPS || "500");
const HOURS = parseFloat(__ENV.DIURNAL_TOTAL_HOURS || "24");

// 24-stage hourly shape. Each entry's `rps` is the target the executor
// ramps to during that stage; ramping-arrival-rate interpolates linearly
// from the previous stage's target.
const HOURLY_RPS = [
  BASE,           // 00:00 trough
  BASE,
  BASE,
  BASE,
  Math.round(BASE * 1.5),  // 04:00 pre-dawn creep
  Math.round(BASE * 2),
  Math.round(PEAK * 0.4),  // 06:00 morning ramp
  Math.round(PEAK * 0.7),
  Math.round(PEAK * 0.9),
  PEAK,                    // 09:00 morning peak
  Math.round(PEAK * 0.85),
  Math.round(PEAK * 0.9),
  SPIKE,                   // 12:00 lunch spike
  Math.round(PEAK * 0.85),
  Math.round(PEAK * 0.9),
  PEAK,                    // 15:00 afternoon peak
  SPIKE,                   // 16:00 afternoon spike
  Math.round(PEAK * 0.8),
  Math.round(PEAK * 0.6),  // 18:00 evening decline
  Math.round(PEAK * 0.4),
  Math.round(PEAK * 0.3),
  Math.round(BASE * 3),
  Math.round(BASE * 2),
  BASE,                    // 23:00 back to trough
];

// Per-stage duration: total_hours / 24, in seconds. Floor to int seconds.
// k6 silently rejects non-integer second strings.
const STAGE_DURATION_S = Math.max(1, Math.round((HOURS * 3600) / 24));

const stages = HOURLY_RPS.map((rps) => ({
  target: rps,
  duration: `${STAGE_DURATION_S}s`,
}));

export const options = {
  scenarios: {
    diurnal: {
      executor: "ramping-arrival-rate",
      startRate: BASE,
      timeUnit: "1s",
      preAllocatedVUs: 100,
      maxVUs: 600,
      stages,
    },
  },
  thresholds: {
    // Permissive thresholds — this scenario's purpose is the forecast-
    // accuracy comparison, not a pass/fail gate. We still want catastrophic
    // regressions (>50% failures) to fail the run.
    "http_req_failed{url:agentic}":   ["rate<0.10"],
    "http_req_failed{url:hpa}":       ["rate<0.50"],
    "http_req_duration{url:agentic}": ["p(95)<3000"],
    "http_req_duration{url:hpa}":     ["p(95)<3000"],
  },
};

export default function () {
  const resA = http.post(workURL(targets.agentic), null, { tags: { url: "agentic" } });
  check(resA, { "agentic status 2xx or 503": (r) => r.status === 200 || r.status === 503 });
  const resH = http.post(workURL(targets.hpa), null, { tags: { url: "hpa" } });
  check(resH, { "hpa status 2xx or 503": (r) => r.status === 200 || r.status === 503 });
}
