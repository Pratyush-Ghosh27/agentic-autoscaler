// Varied scenario — 24h continuously-varying compound wave + periodic
// bursts, designed to satisfy BOTH demo claims simultaneously:
//
//   1. "predicted_rps ≈ actual_rps" — for every second of the run, not
//      just for flat phases. The baseline is a never-flat sum of three
//      sinusoids + deterministic noise, with max slope ≈ 8 RPS/min so
//      Prophet's trend+changepoint model tracks it cleanly.
//   2. "AAS 503 rate ≪ HPA 503 rate" — periodic +180 RPS bursts (90s
//      each, every 30 min) overload HPA's cost-tight pods (averageValue
//      =55) but sit inside AAS's headroom (rpsPerPodMax=31 → ~43% util).
//
// Hackathon-six branch addition. Forked from hackathon-two. The earlier
// scenarios in this repo solve only one half each:
//
//   diurnal.js  — hour-aligned ramps, predicted ≈ actual yes, but the
//                 gradual transitions give HPA plenty of time to react
//                 → minimal 503 differential.
//   rotating.js — predicted ≈ actual yes (narrow [60, 220] band), but
//                 nobody ever 503s — both scalers sit comfortably below
//                 capacity throughout. Demo has no 503-gap story.
//   stress.js   — large 503 gap, but the amplitude is so wide that
//                 predicted_rps visibly lags actual on the dashboard
//                 during every spike → fails the prediction claim.
//   schedule.js — solves both claims on hackathon-five, but only via
//                 long flat hour-aligned phases. "Predicted ≈ actual"
//                 on a flat phase is the trivial case; this scenario
//                 demonstrates it on continuous motion instead.
//
// Baseline shape — always moving, never flat:
//
//   baseline(t) = 240
//               +  50 · sin(2π · t / 240 min)    // slow drift, 4h period
//               +  35 · sin(2π · t /  60 min)    // mid wave,   1h period
//               +  20 · sin(2π · t /  17 min)    // fast wave,  17m period
//               +   8 · lcg_noise(t)             // ±8 RPS deterministic
//
// Properties (t in minutes):
//   - mean              240 RPS
//   - range            ~[125, 355]
//   - max slope        ≈ 7.5 RPS/min  (dominated by the 17-min fast wave;
//                                       max_slope = 2π·20/17 ≈ 7.4)
//   - period           non-hour-aligned (17/60/240 share no common period
//                       with 24h or with each other) → no hour_baseline
//                       false-positive even if PROPHET_USE_HOURLY_REGRESSOR
//                       were enabled.
//
// Burst layer — the HPA-kill events:
//
//   Every VARIED_BURST_INTERVAL_MIN (default 30) minutes, starting at
//   t=30 (skipping t=0 to give the forecaster a 30-min warm-up window),
//   a 90-second pulse:
//
//     0-20 s   ramp:  baseline → baseline + VARIED_BURST_HEIGHT_RPS (180)
//     20-70 s  hold:  baseline + 180 (50s plateau, longer than HPA's
//                     60s stabilizationWindow → HPA's first scale-up
//                     decision is taken DURING the burst, not after)
//     70-90 s  decay: → baseline (or whatever the wave moved to in 90s)
//
//   First burst at t=30 min, last at t=24h - 30 min = 1410 min, total
//   47 bursts per 24h. Override VARIED_BURST_INTERVAL_MIN to change.
//
// Per-burst replica/503 math (HPA averageValue=55, AAS rpsPerPodMax=31,
// at baseline=240, burst peak=420):
//
//                                  HPA (cost-tight)     AAS (cost-loose)
//   pods at T-30s (baseline)       ceil(240/55) = 5     ceil(240/31) = 8
//   capacity at T-30s              5 × 70 = 350 RPS     8 × 70 = 560 RPS
//   per-pod RPS at burst peak      420/5 = 84 (120%)    420/8 = 52 (75%)
//   503s during 50s hold           heavy → ~2-4k        none → ~0
//   pods at T+90s (after react)    ~9 (HPA caught up)   ~8 (unchanged)
//
// Over 24h, 47 bursts × ~2-4k 503s each → HPA produces ~100-200k 503s;
// AAS produces ~0-500 503s (a handful during the slowest baseline
// troughs if any). 503 ratio ≈ 200-1000×.
//
// Tunables (all optional, defaults shown):
//   VARIED_TOTAL_HOURS         "24"   total run duration in hours
//   VARIED_BASELINE_MEAN       "240"  centre of the compound wave
//   VARIED_DRIFT_AMP           "50"   slow drift amplitude (4h period)
//   VARIED_MID_AMP             "35"   mid wave amplitude   (1h period)
//   VARIED_FAST_AMP            "20"   fast wave amplitude  (17m period)
//   VARIED_NOISE_AMP           "8"    LCG noise amplitude  (per-minute)
//   VARIED_BURST_INTERVAL_MIN  "30"   minutes between burst onsets
//   VARIED_BURST_HEIGHT_RPS    "180"  burst peak above baseline
//   VARIED_BURST_RAMP_SEC      "20"   burst ramp-up duration
//   VARIED_BURST_HOLD_SEC      "50"   burst plateau duration
//   VARIED_BURST_DECAY_SEC     "20"   burst decay duration
//
// Examples:
//   make k6-incluster-varied                              # 24h, 47 bursts
//   VARIED_TOTAL_HOURS=1 make k6-incluster-varied         # 1h smoke (1 burst at t=30)
//   VARIED_BURST_HEIGHT_RPS=250 make k6-incluster-varied  # taller bursts
//   VARIED_BURST_INTERVAL_MIN=15 make k6-incluster-varied # bursts twice as often

import http from "k6/http";
import { check } from "k6";
import { getTargets, workURL } from "../lib/targets.js";

const targets = getTargets();

const TOTAL_HOURS        = parseFloat(__ENV.VARIED_TOTAL_HOURS        || "24");
const BASELINE_MEAN      = parseFloat(__ENV.VARIED_BASELINE_MEAN      || "240");
const DRIFT_AMP          = parseFloat(__ENV.VARIED_DRIFT_AMP          || "50");
const MID_AMP            = parseFloat(__ENV.VARIED_MID_AMP            || "35");
const FAST_AMP           = parseFloat(__ENV.VARIED_FAST_AMP           || "20");
const NOISE_AMP          = parseFloat(__ENV.VARIED_NOISE_AMP          || "8");
const BURST_INTERVAL_MIN = parseFloat(__ENV.VARIED_BURST_INTERVAL_MIN || "30");
const BURST_HEIGHT_RPS   = parseFloat(__ENV.VARIED_BURST_HEIGHT_RPS   || "180");
const BURST_RAMP_SEC     = parseFloat(__ENV.VARIED_BURST_RAMP_SEC     || "20");
const BURST_HOLD_SEC     = parseFloat(__ENV.VARIED_BURST_HOLD_SEC     || "50");
const BURST_DECAY_SEC    = parseFloat(__ENV.VARIED_BURST_DECAY_SEC    || "20");

// Periods of the three sinusoids, in minutes. Chosen so their LCM is much
// larger than the 24h run (17 and 60 share no factors; 60 and 240 share
// only 60). The combined wave therefore never exactly repeats inside the
// scenario — gives Prophet a "novel" trajectory to track every cycle.
const DRIFT_PERIOD_MIN = 240;
const MID_PERIOD_MIN   = 60;
const FAST_PERIOD_MIN  = 17;

// Deterministic per-minute noise. LCG with a constant seed so two
// back-to-back runs produce byte-identical traffic — important for
// cross-run AAS-vs-HPA comparison (any difference in 503 counts is
// attributable to the controller's decisions, not to traffic jitter).
function lcgNoise(minuteIndex) {
  let s = (1103515245 ^ Math.floor(minuteIndex)) & 0x7fffffff;
  s = (s * 1103515245 + 12345) & 0x7fffffff;
  // Map [0, 2^31) → [-1, +1] roughly.
  return (s / 1073741824) - 1;
}

// baseline(t) — the compound-wave RPS at time t (minutes). Always
// positive: BASELINE_MEAN=240 dominates the ±113 RPS combined-amplitude
// envelope. The Math.max(1, ...) is a defensive floor in case a user
// overrides the parameters into a regime where the wave could dip below
// zero — k6's ramping-arrival-rate refuses negative targets.
function baseline(tMinutes) {
  const drift = DRIFT_AMP * Math.sin(2 * Math.PI * tMinutes / DRIFT_PERIOD_MIN);
  const mid   = MID_AMP   * Math.sin(2 * Math.PI * tMinutes / MID_PERIOD_MIN);
  const fast  = FAST_AMP  * Math.sin(2 * Math.PI * tMinutes / FAST_PERIOD_MIN);
  const noise = NOISE_AMP * lcgNoise(tMinutes);
  return Math.max(1, Math.round(BASELINE_MEAN + drift + mid + fast + noise));
}

// Convert a duration in minutes to a k6 stage-duration string. Bursts
// produce non-integer-minute durations (e.g. 1.5min for a 90s burst,
// or 0.5min for a pre-burst preamble); the function rounds to whole
// seconds and emits either "Nm" or "Ns" depending on cleanliness.
function durToK6(minutes) {
  const sec = Math.max(1, Math.round(minutes * 60));
  if (sec % 60 === 0) return (sec / 60) + "m";
  return sec + "s";
}

// Build the full stages array. Maintains TWO independent cursors so the
// baseline-minute grid and the burst schedule both fire on time:
//
//   - `t`               : current wall-clock minute (advances by stages)
//   - `nextBurstStart`  : wall-clock minute of the NEXT burst onset
//                         (starts at BURST_INTERVAL_MIN, increments by
//                         BURST_INTERVAL_MIN every time we emit a burst)
//
// Each loop iteration emits exactly one of these:
//
//   A. A burst (3 sub-minute stages) — when the next burst onset is
//      inside the upcoming 1-minute window. May be preceded by a short
//      preamble baseline stage if `t` isn't already at `nextBurstStart`.
//   B. A baseline minute stage — when no burst is due in the next minute.
//
// This is the "earliest of two landmarks" pattern; it keeps bursts
// firing on their absolute schedule (e.g. t=30, 60, 90, ...) even though
// each burst displaces 1.5 min of wall-clock from the surrounding
// baseline-minute grid.
function buildStages() {
  const stages = [];
  const totalMin = TOTAL_HOURS * 60;
  const burstDurationMin = (BURST_RAMP_SEC + BURST_HOLD_SEC + BURST_DECAY_SEC) / 60;
  let t = 0;
  let nextBurstStart = BURST_INTERVAL_MIN; // first burst at t=interval (skip t=0)
  const eps = 1e-6;
  while (t + eps < totalMin) {
    const oneMinuteAhead = t + 1;
    const burstFitsBeforeEnd = (nextBurstStart + burstDurationMin <= totalMin);
    const burstIsImminent = burstFitsBeforeEnd && (nextBurstStart < oneMinuteAhead - eps);
    if (burstIsImminent) {
      // Emit any pre-burst baseline preamble (zero-length if the burst
      // landmark is exactly at `t`, i.e. the previous stage ended on a
      // burst-aligned minute boundary).
      if (nextBurstStart > t + eps) {
        const preDur = nextBurstStart - t;
        stages.push({ target: baseline(nextBurstStart), duration: durToK6(preDur) });
        t = nextBurstStart;
      }
      // Burst: 3 stages totalling burstDurationMin minutes.
      const burstPeak = baseline(t) + BURST_HEIGHT_RPS;
      const afterBurst = t + burstDurationMin;
      stages.push({ target: burstPeak,             duration: BURST_RAMP_SEC  + "s" });
      stages.push({ target: burstPeak,             duration: BURST_HOLD_SEC  + "s" });
      stages.push({ target: baseline(afterBurst),  duration: BURST_DECAY_SEC + "s" });
      t = afterBurst;
      nextBurstStart += BURST_INTERVAL_MIN;
    } else {
      // No burst in the next minute window — emit one baseline stage,
      // clipping to `totalMin` so the final stage doesn't overshoot.
      const stageEnd = Math.min(oneMinuteAhead, totalMin);
      const dur = stageEnd - t;
      stages.push({ target: baseline(stageEnd), duration: durToK6(dur) });
      t = stageEnd;
    }
  }
  return stages;
}

export const options = {
  scenarios: {
    varied: {
      executor: "ramping-arrival-rate",
      // startRate = baseline(0) so the very first second of the run is
      // already at the wave's intended level (not ramping up from 0).
      startRate: baseline(0),
      timeUnit: "1s",
      // preAllocatedVUs covers steady-state baseline (~240 RPS × 0.115s
      // work ≈ 28 concurrent). maxVUs covers the burst peak (~420 RPS ×
      // 0.115s ≈ 49 concurrent) with 10× headroom for any baseline-phase
      // alignment that pushes the burst peak higher.
      preAllocatedVUs: 150,
      maxVUs: 600,
      stages: buildStages(),
    },
  },
  thresholds: {
    // Maximally permissive thresholds for the same reason rotating.js
    // uses 0.95: the demo's pass/fail signal lives on the Prometheus
    // dashboards (predicted_rps_vs_actual_rps + 503-rate panels), not
    // in k6's own http_req_failed. A single overloaded HPA pod during
    // a burst could push the overall window's failure rate above 0.50
    // and trip the threshold → exits k6 with rc!=0 → Job marked
    // Failed → no retry → "the 23h run disappeared". 0.95 is well
    // above any realistic outcome and reserves the threshold for
    // "k6 is fundamentally broken" failures only.
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
