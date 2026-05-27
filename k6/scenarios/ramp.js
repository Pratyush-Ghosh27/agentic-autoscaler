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
    // Ramp tolerates a higher 5xx ceiling than steady because the first
    // ~minute of ramp-up arrives at the targets before either autoscaler can
    // fully catch up: even with the workflow's 12m steady-state warm-up that
    // gets AAS past HOT_PATH_MIN_POINTS=10, there is structural saturation
    // during the 0→PEAK phase (initial replicas absorb the leading edge of
    // the ramp before scale-up lands). 0.10 matches spiky (the closest peer
    // scenario by ramp-difficulty); steady remains stricter at 0.05.
    "http_req_failed": ["rate<0.10"],
    "http_req_duration{url:agentic}": ["p(95)<2000"],
    "http_req_duration{url:hpa}": ["p(95)<2000"],
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
