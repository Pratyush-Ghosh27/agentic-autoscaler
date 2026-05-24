// Steady scenario — constant RPS for STEADY_DURATION.
//
// Tests the controller's stability under predictable load: once the
// rps_per_pod estimate converges, replica count should remain flat
// (NoChange events) without thrashing.
//
// Env vars:
//   STEADY_RPS       "100"
//   STEADY_DURATION  "10m"

import http from "k6/http";
import { check } from "k6";
import { getTargets, workURL } from "../lib/targets.js";

const targets = getTargets();

const RPS = parseInt(__ENV.STEADY_RPS || "100");
const DURATION = __ENV.STEADY_DURATION || "10m";

export const options = {
  scenarios: {
    steady: {
      executor: "constant-arrival-rate",
      rate: RPS,
      timeUnit: "1s",
      duration: DURATION,
      preAllocatedVUs: 50,
      maxVUs: 200,
    },
  },
  thresholds: {
    "http_req_failed": ["rate<0.05"],
    "http_req_duration{url:agentic}": ["p(95)<2000"],
    "http_req_duration{url:hpa}": ["p(95)<2000"],
  },
};

export default function () {
  const resA = http.post(workURL(targets.agentic), null, {
    tags: { url: "agentic" },
  });
  check(resA, { "agentic ok": (r) => r.status === 200 || r.status === 503 });

  const resH = http.post(workURL(targets.hpa), null, {
    tags: { url: "hpa" },
  });
  check(resH, { "hpa ok": (r) => r.status === 200 || r.status === 503 });
}
