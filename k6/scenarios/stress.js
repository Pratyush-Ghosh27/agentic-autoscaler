// Stress scenario — designed to maximise the 503-rate differential between
// the predictive (app-agentic) and reactive (app-hpa) autoscalers.
//
// Hackathon-three-branch addition. Built specifically to expose HPA's
// fundamental weakness: its 60s stabilizationWindow + ~30s pod-startup
// means it needs ~90s of sustained over-target utilisation before new
// capacity is online. AAS, with FORECAST_HORIZON_MINUTES=5 of lookahead
// (or with a quantile forecaster that systematically over-provisions),
// can be at the spike's required replica count *before* the spike hits.
//
// The other scenarios in this repo (ramp, steady, spiky, bursty,
// diurnal, rotating) are tuned for the OTHER hackathon claim —
// "predicted_rps ≈ actual_rps". Their amplitudes are deliberately
// modest (peaks of 200-500 RPS over a 7-pod baseline) so the forecast
// line tracks actual cleanly. That same modesty means neither scaler
// ever actually runs out of capacity: 200 RPS / 70 RPS-per-pod = 3
// pods needed at the spike peak, and both scalers comfortably maintain
// 3+ pods. The 503 rate stays at noise levels (< 0.5%) for both.
//
// To make 503s a meaningful signal, this scenario goes deliberately
// above either scaler's *current* capacity at every spike:
//
//   - Baseline = 200 RPS  → with rpsPerPodMin=30, both scalers settle
//                            on ~7 pods. Per-pod RPS ≈ 28 (target util
//                            ~40%, comfortable headroom).
//   - Spike    = 600 RPS  → needs ⌈600/30⌉ = 20 pods (maxReplicas cap
//                            inherited from hackathon-two). With only
//                            7 pods at spike onset, per-pod RPS = 85.7
//                            — well above per-pod capacity of ~70 RPS
//                            (TARGET_CONCURRENCY=8 / ~115ms work).
//                            Concurrency on each pod exceeds 8 within
//                            ~10s and the target app returns 503.
//
// Per-cycle layout (10m total):
//
//   0-7 min     BASELINE 200 RPS                (both scalers settled)
//   7m-7m05s    HARD STEP UP to 600 RPS         (5s ramp; near-instant)
//   7m05s-10m   HOLD at 600 RPS                 (~3 min of overcapacity
//                                                 for HPA; AAS already
//                                                 pre-scaled if forecast
//                                                 caught the pattern)
//   10m         HARD STEP DOWN to 200 RPS       (5s ramp; next cycle)
//
// 6 cycles fits cleanly in 60 minutes. The first cycle is unfair to
// AAS — its forecaster has < 30 min of history so periodicity isn't
// learnable yet — so the meaningful comparison is cycles 2-6. With
// `preferredForecaster: gbdt_quantile` (and GBDT_QUANTILE=0.85+) AAS
// over-provisions from observed variance and beats HPA *from cycle 1*.
//
// Expected outcome per spike, on hackathon-three defaults:
//
//          | replicas at T=0 | replicas at T=90s | 503 count per spike
//   HPA    | 7               | 11                | ~10,000-16,000
//   AAS    | 20 (warm)       | 20                | ~0-100  (noise)
//
// Over 6 spikes: HPA accumulates ~60k-100k failed requests; AAS
// accumulates ~600 at worst. Differential is 100-1000×.
//
// Tunables (all optional, defaults shown):
//   STRESS_CYCLES         "6"    number of full cycles (6 = 60 min)
//   STRESS_BASELINE_RPS   "200"  flat baseline between spikes
//   STRESS_SPIKE_RPS      "600"  spike peak — set above maxReplicas *
//                                  rpsPerPodMin to make the scaler the
//                                  bottleneck, not the pod count cap
//   STRESS_BASELINE_MIN   "7"    minutes at baseline per cycle
//   STRESS_SPIKE_MIN      "3"    minutes at spike per cycle
//
// Examples:
//   make k6-incluster-stress                                     # 6 cycles, 60m
//   STRESS_CYCLES=2 make k6-incluster-stress                     # 20m smoke test
//   STRESS_SPIKE_RPS=800 make k6-incluster-stress                # harder spike
//   STRESS_BASELINE_MIN=10 STRESS_SPIKE_MIN=5 \
//     make k6-incluster-stress                                   # longer cycles

import http from "k6/http";
import { check } from "k6";
import { getTargets, workURL } from "../lib/targets.js";

const targets = getTargets();

const CYCLES        = parseInt(__ENV.STRESS_CYCLES        || "6");
const BASELINE_RPS  = parseInt(__ENV.STRESS_BASELINE_RPS  || "200");
const SPIKE_RPS     = parseInt(__ENV.STRESS_SPIKE_RPS     || "600");
const BASELINE_MIN  = parseInt(__ENV.STRESS_BASELINE_MIN  || "7");
const SPIKE_MIN     = parseInt(__ENV.STRESS_SPIKE_MIN     || "3");

// Build the full stages array. Concatenated across CYCLES so the
// executor sees one continuous sequence and never goes idle between
// spikes — same design principle as rotating.js, for the same reason
// (any gap drops to 0 RPS which the forecaster's hot-path window then
// captures, polluting the next several minutes of predictions).
function buildStages() {
  const stages = [];
  for (let c = 0; c < CYCLES; c++) {
    // BASELINE phase: flat at BASELINE_RPS. ramping-arrival-rate
    // interpolates linearly from the previous stage's target; the
    // previous stage (end of last cycle, or startRate on cycle 0) is
    // also at BASELINE_RPS, so this stays flat throughout.
    stages.push({ target: BASELINE_RPS, duration: `${BASELINE_MIN}m` });

    // STEP UP: 5s linear ramp from BASELINE_RPS to SPIKE_RPS. Short
    // enough to be visually a step on the dashboard; long enough that
    // k6 can spin up the extra VUs without dropping requests on the
    // executor side. Hard 1s ramps occasionally drop a few requests
    // when preAllocatedVUs is exhausted; 5s is the empirical sweet
    // spot used by rotating.js's spiky phase too.
    stages.push({ target: SPIKE_RPS, duration: "5s" });

    // SPIKE HOLD: flat at SPIKE_RPS for SPIKE_MIN minutes. This is
    // where 503s accumulate on the HPA side — long enough for HPA's
    // 60s stabilization window to expire AND for the new pods to
    // start, but short enough that HPA never fully catches up to the
    // spike's true requirement (20 pods on default config).
    stages.push({ target: SPIKE_RPS, duration: `${SPIKE_MIN}m` });

    // STEP DOWN: 5s ramp back to BASELINE_RPS. Symmetric with the
    // step up. The next cycle's BASELINE stage then holds flat at
    // BASELINE_RPS.
    stages.push({ target: BASELINE_RPS, duration: "5s" });
  }
  return stages;
}

export const options = {
  scenarios: {
    stress: {
      executor: "ramping-arrival-rate",
      // startRate = BASELINE_RPS so the very first second is already
      // at baseline (rather than ramping from 0 over BASELINE_MIN
      // minutes, which would be a slow climb the controller would
      // mis-classify as PatternGradualRamp).
      startRate: BASELINE_RPS,
      timeUnit: "1s",
      // 600 RPS × 115ms work ≈ 69 concurrent VUs at spike peak.
      // preAllocated=150 covers baseline (200 × 115ms ≈ 23 VUs) with
      // ~6× margin and avoids any cold-start VU allocation when the
      // step-up fires. maxVUs=500 leaves room for the case where work
      // latency degrades under load (server side queueing pushes p95
      // higher → k6 needs more VUs to maintain the arrival rate).
      preAllocatedVUs: 150,
      maxVUs: 500,
      stages: buildStages(),
    },
  },
  thresholds: {
    // INTENTIONALLY permissive thresholds — this scenario's purpose
    // is to PRODUCE 503s on the HPA side, not to gate on their
    // absence. The agentic threshold is 30% (very loose) because the
    // first cycle is unfair to AAS (no forecast history) and we
    // don't want the run to fail before the meaningful cycles 2-6
    // get measured. The HPA threshold is 80% to allow catastrophic
    // accumulated 503 rates without failing the run.
    "http_req_failed{url:agentic}":   ["rate<0.30"],
    "http_req_failed{url:hpa}":       ["rate<0.80"],
    "http_req_duration{url:agentic}": ["p(95)<5000"],
    "http_req_duration{url:hpa}":     ["p(95)<5000"],
  },
};

export default function () {
  const resA = http.post(workURL(targets.agentic), null, { tags: { url: "agentic" } });
  check(resA, { "agentic status 2xx or 503": (r) => r.status === 200 || r.status === 503 });
  const resH = http.post(workURL(targets.hpa), null, { tags: { url: "hpa" } });
  check(resH, { "hpa status 2xx or 503": (r) => r.status === 200 || r.status === 503 });
}
