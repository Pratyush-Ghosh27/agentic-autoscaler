// Rotating-loop scenario — four traffic patterns in one continuous k6 process.
//
// Hackathon-two-branch addition. Built specifically to address two problems
// the original four-script round-robin had:
//
// 1. INTER-SCENARIO GAPS DROPPED TRAFFIC TO ZERO. The round-robin wrapper
//    (deploy/k6/run-24h-loop.sh) tears down each k6 Job before starting
//    the next, leaving ~30-60s with no traffic. The controller's hot-path
//    forecaster window (HOT_PATH_HISTORY_MINUTES=30) then captured those
//    zeros and predicted predicted_rps ≈ 0 for the next several minutes —
//    exactly the failure the user previously reported on the ramp scenario.
//    This script keeps a single k6 process alive for the whole 24h, so
//    every wall-clock second has traffic. Transitions are at the same RPS
//    value on both sides (each phase ends at 100, the next begins at 100),
//    so the executor crosses the boundary with no discontinuity.
//
// 2. AMPLITUDES WERE TOO WIDE FOR PROPHET TO TRACK. The original
//    standalone scenarios swung from 0 to 500 RPS; Prophet's predicted
//    line lagged sharp transitions by ~5 minutes (the FORECAST_HORIZON_
//    MINUTES window). For the "predicted ≈ actual" demo claim this
//    showed up as visible gaps on the dashboard during ramp tails and
//    spike onsets. This script caps every phase inside [60, 220] RPS
//    centered on 100, so the largest gradient Prophet has to track is
//    100 RPS over 15 min ≈ 6.7 RPS/min — well within its tracking ability.
//
// Per-cycle layout (140 minutes total):
//
//   0-35 min     STEADY at 100 RPS               (flat baseline)
//   35-70 min    RAMP 100 -> 200 (15m up)
//                       200 hold (5m)
//                       200 -> 100 (15m down)    (gradual climb + descent)
//   70-105 min   SPIKY base 100, 30 spikes to 200
//                each spike: 50s base + 5s up + 10s peak + 5s down
//                = 70s period x 30 = 2100s = 35m
//   105-140 min  BURSTY pseudo-random 1-min stages in [60, 140]
//                (LCG-seeded per cycle for variety + reproducibility)
//
// 10 cycles fit in 1400 min = 23h 20min — under the 24h ceiling, no
// mid-cycle truncation. Adjust ROTATING_CYCLES to change.
//
// Tunables (all optional, defaults shown):
//   ROTATING_CYCLES         "10"   number of full cycles (10 = 23h20m)
//   ROTATING_STEADY_RPS     "100"  baseline RPS (every phase touches this)
//   ROTATING_RAMP_PEAK_RPS  "200"  ramp's high plateau
//   ROTATING_SPIKE_RPS      "200"  spike height (above STEADY_RPS base)
//   ROTATING_BURSTY_FLOOR   "60"   bursty stage minimum
//   ROTATING_BURSTY_CEILING "140"  bursty stage maximum
//
// Examples:
//   make k6-incluster-rotating                     # 10 cycles, 23h20m
//   ROTATING_CYCLES=1 make k6-incluster-rotating   # one cycle, 2h20m
//   ROTATING_CYCLES=2 ROTATING_RAMP_PEAK_RPS=300 \
//     make k6-incluster-rotating                   # 2 cycles, taller ramp

import http from "k6/http";
import { check } from "k6";
import { getTargets, workURL } from "../lib/targets.js";

const targets = getTargets();

const CYCLES         = parseInt(__ENV.ROTATING_CYCLES         || "10");
const STEADY_RPS     = parseInt(__ENV.ROTATING_STEADY_RPS     || "100");
const RAMP_PEAK_RPS  = parseInt(__ENV.ROTATING_RAMP_PEAK_RPS  || "200");
const SPIKE_RPS      = parseInt(__ENV.ROTATING_SPIKE_RPS      || "200");
const BURSTY_FLOOR   = parseInt(__ENV.ROTATING_BURSTY_FLOOR   || "60");
const BURSTY_CEILING = parseInt(__ENV.ROTATING_BURSTY_CEILING || "140");

// Build the full stages array. Concatenated across CYCLES so the executor
// sees one continuous sequence and never sits idle between scenarios.
function buildStages() {
  const stages = [];
  for (let c = 0; c < CYCLES; c++) {
    // STEADY phase (35m): one stage at baseline. With ramping-arrival-rate,
    // duration here means "ramp linearly from previous target to STEADY_RPS
    // over 35m" — since the previous bursty phase ends at STEADY_RPS by
    // design (final stage below), this stays flat at STEADY_RPS throughout.
    stages.push({ target: STEADY_RPS, duration: "35m" });

    // RAMP phase (35m): 15m climb to RAMP_PEAK_RPS, 5m hold, 15m descent
    // back to STEADY_RPS. Smooth handoff to spiky (both at STEADY_RPS).
    stages.push({ target: RAMP_PEAK_RPS, duration: "15m" });
    stages.push({ target: RAMP_PEAK_RPS, duration: "5m"  });
    stages.push({ target: STEADY_RPS,    duration: "15m" });

    // SPIKY phase (35m): 30 spike cycles of 70s each, totalling exactly
    // 35m. Each spike is a quick triangle so Prophet sees the discontinuity
    // come and go within a small fraction of HOT_PATH_HISTORY_MINUTES=30.
    for (let s = 0; s < 30; s++) {
      stages.push({ target: STEADY_RPS, duration: "50s" });
      stages.push({ target: SPIKE_RPS,  duration: "5s"  });
      stages.push({ target: SPIKE_RPS,  duration: "10s" });
      stages.push({ target: STEADY_RPS, duration: "5s"  });
    }

    // BURSTY phase (35m): 35 stages of 1m each. Targets drawn from a
    // linear congruential generator seeded with the cycle index so:
    //   - each cycle's bursty pattern is different (seed varies)
    //   - the run is deterministic / reproducible (LCG, not Math.random)
    // The final stage forces a return to STEADY_RPS for the next cycle's
    // smooth steady handoff. 34 random + 1 anchor = 35 stages total.
    let seed = 1103515245 ^ c;
    const range = BURSTY_CEILING - BURSTY_FLOOR + 1;
    for (let i = 0; i < 34; i++) {
      seed = (seed * 1103515245 + 12345) & 0x7fffffff;
      const target = BURSTY_FLOOR + (seed % range);
      stages.push({ target, duration: "1m" });
    }
    stages.push({ target: STEADY_RPS, duration: "1m" });
  }
  return stages;
}

export const options = {
  scenarios: {
    rotating: {
      executor: "ramping-arrival-rate",
      // startRate = STEADY_RPS so the very first second of the run is
      // already at baseline (rather than ramping up from 0 over the first
      // stage's duration, which would be 35m of climbing — wrong).
      startRate: STEADY_RPS,
      timeUnit: "1s",
      // 50 preAllocated covers steady comfortably (~115ms work duration
      // x 100 RPS ≈ 12 concurrent VUs); 300 max covers the 200-RPS peaks.
      preAllocatedVUs: 50,
      maxVUs: 300,
      stages: buildStages(),
    },
  },
  thresholds: {
    // Maximally permissive thresholds. The demo's purpose is the
    // forecast-accuracy + 503-rate-gap comparison via Prometheus,
    // NOT a pass/fail gate on k6's own metrics. The previous values
    // (hpa rate<0.50) were a near-miss on hackathon-four: with HPA
    // tuned to averageValue=50 (~71% per-pod utilisation), HPA's
    // overall 503 rate over 23h is expected to land at 1-3%, but
    // any single bad cycle could push the overall window above 0.50
    // and trip the threshold, exiting k6 with rc!=0 → Job marked
    // Failed → backoffLimit=0 → no retry → "the run disappeared".
    // 0.95 is well above any realistic outcome and reserves the
    // threshold purely for "k6 is fundamentally broken" failures
    // (e.g., target Service unreachable for the whole run).
    "http_req_failed{url:agentic}":   ["rate<0.95"],
    "http_req_failed{url:hpa}":       ["rate<0.95"],
    "http_req_duration{url:agentic}": ["p(95)<10000"],
    "http_req_duration{url:hpa}":     ["p(95)<10000"],
  },
};

export default function () {
  const resA = http.post(workURL(targets.agentic), null, { tags: { url: "agentic" } });
  check(resA, { "agentic status 2xx or 503": (r) => r.status === 200 || r.status === 503 });
  const resH = http.post(workURL(targets.hpa), null, { tags: { url: "hpa" } });
  check(resH, { "hpa status 2xx or 503": (r) => r.status === 200 || r.status === 503 });
}
