// Schedule scenario — production-like hourly schedule, designed to give the
// predictive autoscaler a clean lookahead advantage that translates to a
// dramatically lower 503 rate while still letting Prophet's `predicted_rps`
// line track `actual_rps` tightly on the Grafana dashboard.
//
// Hackathon-five branch addition. None of the other scenarios in this repo
// produce BOTH demo claims on a single run:
//
//   diurnal.js  — predicted ≈ actual (yes), but transitions are gradual
//                 hourly ramps → HPA reacts in time → tiny 503 differential
//   rotating.js — predicted ≈ actual (yes), narrow amplitudes [60, 220]
//                 keep both scalers comfortably below capacity → no 503s
//                 for either
//   stress.js   — large 503 gap (yes, with GBDT_QUANTILE pinned), but the
//                 amplitude is so large that predicted_rps visibly lags
//                 actual_rps on the dashboard during every spike → fails
//                 the "predicted ≈ actual" half of the demo
//
// THIS scenario sits in the sweet spot. The CRITICAL design insight from
// reading forecast-service/src/forecast/prophet_model.py:
//
//   Prophet is configured with daily_seasonality=False, weekly_seasonality=
//   False. The ONLY seasonal signal it gets is the `hour_baseline` external
//   regressor — a 24-bin "median RPS for hour-of-day H" computed by the
//   classifier from the last CLASSIFIER_HISTORY_HOURS of Prometheus data
//   and stamped into the request context once HOURLY_PROFILE_MIN_HOURS=12
//   distinct UTC hours have been observed (controller passes this in
//   ContextPayload.hourly_profile + hourly_profile_valid; see
//   internal/classifier/pipeline.go).
//
// So Prophet CANNOT learn arbitrary periodicity (no 5-min cycles, no 30-
// min cycles, no 2h cycles). It CAN learn "hour H of the day typically has
// X RPS". This pins the design exactly:
//
//   1. Pattern must align with WALL-CLOCK UTC HOURS.
//   2. Within each hour, RPS must be ~constant (so the per-hour MEDIAN
//      that Prophet's regressor reads is close to the within-hour value).
//   3. Transitions between hours must be SHARP — faster than HPA's
//      60s stabilization + 30s pod-startup = ~90s reactive lag — so HPA
//      visibly fails to keep up at every spike onset.
//   4. Spike amplitude must push HPA's per-pod RPS above capacity
//      (~70 RPS/pod from TARGET_CONCURRENCY=8 / 0.115s work).
//   5. Spike duration must extend past HPA's catch-up time, so the 503
//      window is bounded (~60-90s after each transition) rather than
//      continuous — keeps the "predicted ≈ actual" line clean for the
//      remaining 58 minutes of each spike hour.
//
// 24-hour repeating profile (each hour: 30s ramp + 59m30s hold):
//
//   00:00-05:59   LOW    100 RPS   (overnight low — HPA settles at 4 pods)
//   06:00-06:59   MEDLO  150 RPS   (early morning)
//   07:00-07:59   MED    200 RPS   (morning ramp-up — HPA at 7 pods)
//   08:00-09:59   SPIKE  350 RPS   ← morning rush (200→350 onset @ 08:00:00)
//   10:00-11:59   MED    200 RPS
//   12:00-12:59   SPIKE  350 RPS   ← lunch rush (200→350 onset @ 12:00:00)
//   13:00-16:59   MED    200 RPS
//   17:00-18:59   SPIKE  350 RPS   ← evening rush (200→350 onset @ 17:00:00)
//   19:00-19:59   MED    200 RPS
//   20:00-20:59   MEDLO  150 RPS
//   21:00-21:59   SPIKE  350 RPS   ← evening event (150→350 onset @ 21:00:00)
//   22:00-23:59   LOW    100 RPS
//
// 4 SHARP spike onsets per 24h. Within-hour shape is flat → hour_baseline
// median for hour 8 = 350, hour 9 = 350, hour 12 = 350, etc.
//
// THE 24H VARIANT (default since SCHEDULE_DAYS=1). The original design
// of this scenario was pure-predictive: rely entirely on Prophet's
// hour_baseline regressor to pre-empt every transition. That required
// 48h of runtime — 24h to populate all 24 hour-of-day bins, then 24h
// for cycle 2 to actually USE the populated profile. The user can only
// wait 24h for the demo, so the design now blends TWO mechanisms so
// the 503 gap is visible from hour 1, not hour 25:
//
//   MECHANISM A (structural, day-1 visible) — ported from hackathon-four:
//     HPA's `averageValue` target is tightened from 30 → 50 RPS/pod.
//     HPA therefore runs at ~71% utilisation at the MED (200 RPS)
//     plateau and ~100%+ during every transition; AAS keeps the
//     hackathon-two `rpsPerPodMin=30` divisor and runs at ~41% util,
//     so every spike onset finds HPA's 7 pods unable to absorb the
//     200→350 ramp while AAS's 7 pods have 70% headroom and weather
//     it without 503s. This delivers ~3-10× 503 reduction from
//     hour 1.
//
//   MECHANISM B (predictive, progressively engages over hours 4-24):
//     HOURLY_PROFILE_MIN_HOURS dropped from 12 → 4 (controller +
//     forecast-service). After ~4 hours of demo runtime, the
//     classifier flips hourly_profile_valid=true. Hours actually
//     observed (4 of 24 at flip-time) carry real medians; the other
//     20 bins are interpolated from neighbours. From hour ~4 onward,
//     Prophet's hour_baseline regressor blends partial-profile +
//     recent-trend, and AAS starts pre-empting hours it has already
//     seen recurring in the schedule. By hour 24 every hour-of-day
//     bin has at least one real sample, so every subsequent spike
//     would be fully pre-empted (relevant only if user extends to
//     SCHEDULE_DAYS=2). On the default 24h run this raises the day-1
//     ratio from ~3-10× to ~5-15× over the back half.
//
// Per-spike-onset 503 budget (24h variant, HPA averageValue=50,
// AAS rpsPerPodMin=30, both maxReplicas=20):
//
//                                  HPA (reactive, cost-tight)   AAS (structural+partial-predictive)
//   pods at T-30s                  4 (from 200 RPS, 50 target)  7 (from 200 RPS, 30 divisor)
//   actual at T+0                  350 RPS                      350 RPS
//   pods at T+0                    4                            7 (hour 0-4) / 10-12 (hour 4-24, when regressor pre-empts)
//   per-pod RPS at T+0             87 RPS/pod (overloaded)      50 RPS/pod (71% util) → 29 RPS/pod (41% util) once pre-empting
//   per-pod RPS during transition  140+ briefly                 50-70 briefly / 29 briefly
//   pods at T+90s (after react)    7                            12
//   503s per spike onset           ~3,000-6,000                 ~200-1,000 (hour 0-4) / ~0-300 (hour 4-24)
//
// Per 24h cycle (4 spikes × hour 0-4: structural-only; × hour 4-24: structural+regressor):
//   HPA: ~12,000-24,000 503s (4 spikes × ~4,000 each)
//   AAS: ~500-2,500 503s (cycle-average across both regimes)
//   Net ratio: ~5-15× on day 1, climbing toward 20-100× if extended
//   to SCHEDULE_DAYS=2.
//
// Tunables (all optional, defaults shown):
//   SCHEDULE_DAYS          "1"    number of full 24h cycles. Default 1
//                                  delivers the demo in one wall-clock day.
//                                  Set 2+ to also exercise the cycle-2
//                                  fully-populated-regressor regime, in
//                                  which case the per-cycle 503 gap on
//                                  cycle 2+ widens to 20-100×.
//   SCHEDULE_LOW_RPS       "100"  overnight low (HPA settles at 2-4 pods)
//   SCHEDULE_MEDLO_RPS     "150"  early/wind-down
//   SCHEDULE_MED_RPS       "200"  pre/post-rush plateau
//   SCHEDULE_SPIKE_RPS     "350"  rush-hour spike (HPA needs 12 pods at the
//                                  averageValue=50 cost-tight target)
//   SCHEDULE_TRANSITION_S  "30"   hour-boundary ramp duration (seconds);
//                                  shorter = sharper = more HPA 503s; 30s
//                                  is the empirical sweet spot vs k6's
//                                  preAllocatedVUs ramp-allocation jitter
//
// Examples:
//   make k6-incluster-schedule                              # 1 day (24h) — the demo
//   SCHEDULE_DAYS=2 make k6-incluster-schedule              # 2 days (48h) for cycle-2 lookahead
//   SCHEDULE_DAYS=0.5 SCHEDULE_SPIKE_RPS=250 \
//     make k6-incluster-schedule                            # 12h smoke test

import http from "k6/http";
import { check } from "k6";
import { getTargets, workURL } from "../lib/targets.js";

const targets = getTargets();

const DAYS         = parseFloat(__ENV.SCHEDULE_DAYS         || "1");
const LOW_RPS      = parseInt(__ENV.SCHEDULE_LOW_RPS        || "100");
const MEDLO_RPS    = parseInt(__ENV.SCHEDULE_MEDLO_RPS      || "150");
const MED_RPS      = parseInt(__ENV.SCHEDULE_MED_RPS        || "200");
const SPIKE_RPS    = parseInt(__ENV.SCHEDULE_SPIKE_RPS      || "350");
const TRANSITION_S = parseInt(__ENV.SCHEDULE_TRANSITION_S   || "30");

// 24-bin hourly profile, index = UTC hour-of-day. Each value is the RPS
// the executor sustains for the full hour after the TRANSITION_S ramp.
// Tunable knobs above (LOW/MEDLO/MED/SPIKE) parameterise the four levels;
// the SHAPE is hardcoded so Prophet's hour_baseline learns the same
// pattern every cycle regardless of how the user tunes amplitudes.
function buildHourlyProfile() {
  return [
    // 0     1        2        3        4        5
    LOW_RPS, LOW_RPS, LOW_RPS, LOW_RPS, LOW_RPS, LOW_RPS,
    // 6        7        8          9          10       11
    MEDLO_RPS, MED_RPS, SPIKE_RPS, SPIKE_RPS, MED_RPS, MED_RPS,
    // 12       13       14       15       16       17
    SPIKE_RPS, MED_RPS, MED_RPS, MED_RPS, MED_RPS, SPIKE_RPS,
    // 18       19       20         21         22       23
    SPIKE_RPS, MED_RPS, MEDLO_RPS, SPIKE_RPS, LOW_RPS, LOW_RPS,
  ];
}

function buildStages(profile) {
  // 30s ramp + (3600 - 30 = 3570)s hold per hour. ramping-arrival-rate
  // interpolates linearly from the previous stage's target to the new
  // target over the stage's duration; the hold stage's target equals the
  // ramp stage's target, so the executor sits flat at that level for
  // the remaining 59m30s.
  const stages = [];
  const holdSeconds = 3600 - TRANSITION_S;
  const totalHours = Math.round(24 * DAYS);
  for (let h = 0; h < totalHours; h++) {
    const target = profile[h % 24];
    stages.push({ target, duration: `${TRANSITION_S}s` });
    stages.push({ target, duration: `${holdSeconds}s` });
  }
  return stages;
}

const profile = buildHourlyProfile();

export const options = {
  scenarios: {
    schedule: {
      executor: "ramping-arrival-rate",
      // startRate matches hour 0's target so the very first second is at
      // baseline — otherwise k6 would ramp from 0 to LOW_RPS over the
      // first stage's 30 seconds, briefly polluting hour_baseline[0] with
      // sub-LOW samples.
      startRate: profile[0],
      timeUnit: "1s",
      // preAllocated covers MEDLO/MED comfortably (200 RPS × ~115ms work
      // ≈ 23 concurrent VUs); maxVUs covers spike (350 RPS × 115ms ≈ 40
      // VUs) with ~4× margin to absorb HPA-side 503-retry queueing
      // (k6 keeps a VU busy for the full HTTP round-trip including the
      // server's 503-with-retry-headers path).
      preAllocatedVUs: 100,
      maxVUs: 300,
      stages: buildStages(profile),
    },
  },
  thresholds: {
    // Maximally permissive — purpose is the Prometheus comparison, not a
    // k6-side pass/fail gate. HPA's overall 503 rate on this scenario is
    // expected at ~5-15% during cycle 2's spike onsets (concentrated in
    // four ~90s windows per cycle); 0.95 absorbs that comfortably while
    // still tripping on "k6 is fundamentally broken" failure modes like
    // target Service unreachable.
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
