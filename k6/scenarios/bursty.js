// Bursty scenario — random burst pattern.
//
// Sends BURST_SIZE requests as fast as possible, then sleeps for a random
// pause within [BURST_MIN_INTERVAL, BURST_MAX_INTERVAL] seconds, repeating
// until BURSTY_TOTAL_DURATION elapses. Models e.g. user-driven traffic
// where a single user-action triggers many backend calls.
//
// The pattern is deliberately not periodic — Prophet should fall back to
// linear extrapolation, and the classifier should land on "spiky" or
// "default" rather than "periodic".
//
// Env vars:
//   BURST_SIZE              "50"
//   BURST_MIN_INTERVAL      "5"   (seconds)
//   BURST_MAX_INTERVAL      "30"  (seconds)
//   BURSTY_TOTAL_DURATION   "15m"

import http from "k6/http";
import { check, sleep } from "k6";
import { getTargets, workURL } from "../lib/targets.js";

const targets = getTargets();

const BURST_SIZE = parseInt(__ENV.BURST_SIZE || "50");
const MIN_INTERVAL = parseInt(__ENV.BURST_MIN_INTERVAL || "5");
const MAX_INTERVAL = parseInt(__ENV.BURST_MAX_INTERVAL || "30");
const TOTAL_DURATION = __ENV.BURSTY_TOTAL_DURATION || "15m";

export const options = {
  scenarios: {
    bursty: {
      executor: "per-vu-iterations",
      vus: 1,
      iterations: parseInt(__ENV.BURSTY_ITERATIONS || "10000"),
      maxDuration: TOTAL_DURATION,
      exec: "burst_loop",
    },
  },
  thresholds: {
    "http_req_failed": ["rate<0.15"],
  },
};

export function burst_loop() {
  for (let i = 0; i < BURST_SIZE; i++) {
    const resA = http.post(workURL(targets.agentic), null, {
      tags: { url: "agentic" },
    });
    check(resA, {
      "agentic burst ok": (r) => r.status === 200 || r.status === 503,
    });
    const resH = http.post(workURL(targets.hpa), null, {
      tags: { url: "hpa" },
    });
    check(resH, {
      "hpa burst ok": (r) => r.status === 200 || r.status === 503,
    });
  }

  const pause = MIN_INTERVAL + Math.random() * (MAX_INTERVAL - MIN_INTERVAL);
  sleep(pause);
}
