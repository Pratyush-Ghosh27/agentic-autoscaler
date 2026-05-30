// Hourly-cycle scenario — 60-minute periodic profile designed to lift
// hourly autocorrelation above the classifier's 0.70 threshold so the
// AAS classifier auto-selects Prophet (PatternPeriodic → prophet) after
// the warm-up window, without any operator pinning. Hackathon-seven
// addition. Built to address two specific properties the rotating
// scenario could not satisfy at the same time:
//
// 1. ROTATING'S 140-MIN CYCLE ANTI-CORRELATES AT 60-MIN LAG.
//    HourlyAutocorr (internal/classifier/features.go) compares each
//    sample against the sample exactly 60 minutes earlier. With a
//    140-min cycle the 60-min lag lands in a *different* phase of the
//    cycle every time, so autocorr hovers around zero — the classifier
//    settles on PatternGradualRamp (linear_extrap), and Prophet never
//    engages. This script makes the cycle 60 minutes exactly: every
//    sample's 60-min-prior twin is the SAME phase of the SAME cycle,
//    so HourlyAutocorr converges on ~0.97. PatternPeriodic fires →
//    classifier maps to prophet (internal/classifier/params.go).
//
// 2. STILL HAS TO BE VISIBLY DIVERSE FOR THE DEMO.
//    A flat sinusoid would also score >0.70 on hourly autocorr but
//    would be visually uninteresting on Grafana — the predicted-vs-
//    actual overlay only impresses when the workload changes shape
//    abruptly and Prophet still tracks it. This script packs seven
//    qualitatively-distinct phases into each hour (calm, gradient
//    climb, square-wave chaos, plateau, sinusoidal wave, descending
//    tail, quiet) so the recording captures Prophet handling each one,
//    not just one shape repeated 24 times.
//
// Per-cycle layout (60 minutes total):
//
//   00:00-05:00  CALM      flat at HOURLY_CALM_RPS (default 80)
//   05:00-15:00  RAMP      linear climb to HOURLY_PEAK_RPS (default 250)
//   15:00-25:00  CHAOS     square-wave alternating LOW↔HIGH every 50s
//                            LOW  = HOURLY_CHAOS_LOW_RPS  (default 100)
//                            HIGH = HOURLY_CHAOS_HIGH_RPS (default 400)
//                            6 cycles × 100s each = 10 min
//   25:00-33:00  PLATEAU   flat at HOURLY_PLATEAU_RPS (default 280)
//   33:00-45:00  WAVE      discretised sinusoid centred on
//                            HOURLY_WAVE_CENTER_RPS (default 250),
//                            amplitude HOURLY_WAVE_AMPLITUDE (default 60),
//                            5-min period, 12 × 1-min stages
//   45:00-53:00  TAIL      linear descent to HOURLY_QUIET_RPS (default 90)
//   53:00-60:00  QUIET     flat at HOURLY_QUIET_RPS (default 90)
//
// HOURLY_CYCLES cycles fit in HOURLY_CYCLES * 60 min of clock time.
// 24 cycles = 24h (matches the controller's CLASSIFIER_HISTORY_HOURS).
//
// Classifier expectation: 6h of history is the minimum the worker
// needs to compute features (MinPoints=72 at 5-min resolution =
// 6h * 60 / 5). At 6h:
//   - HourlyAutocorr already > 0.90 (one full cycle covered).
//   - PeakToTrough = (max ~400 / min ~80) ≈ 5x → triggers periodic
//     classification (CV is also high from the chaos phase).
//   - Pattern = PatternPeriodic → preferredForecaster = prophet.
// After 12h the classifier has 2 cycles in view; Prophet's
// changepoint detection has stabilised; the predicted line tracks
// each phase within ~30s of the transition.
//
// Tunables (all optional, defaults shown):
//   HOURLY_CYCLES                "24"   number of 60-min cycles (24 = 24h)
//   HOURLY_CALM_RPS              "80"   phase A baseline
//   HOURLY_PEAK_RPS              "250"  phase B ramp target
//   HOURLY_CHAOS_LOW_RPS         "100"  phase C low rung
//   HOURLY_CHAOS_HIGH_RPS        "400"  phase C high rung
//   HOURLY_PLATEAU_RPS           "280"  phase D flat plateau
//   HOURLY_WAVE_CENTER_RPS       "250"  phase E sinusoid centre
//   HOURLY_WAVE_AMPLITUDE        "60"   phase E sinusoid amplitude
//   HOURLY_QUIET_RPS             "90"   phase F descent target + phase G floor
//
// Examples:
//   make k6-incluster-hourly                     # 24 cycles, 24h
//   HOURLY_CYCLES=2 make k6-incluster-hourly     # 2 cycles, 2h (smoke)
//   HOURLY_CYCLES=12 HOURLY_PEAK_RPS=300 \
//     make k6-incluster-hourly                   # 12h, taller ramp

import http from "k6/http";
import { check } from "k6";
import { getTargets, workURL } from "../lib/targets.js";

const targets = getTargets();

const CYCLES            = parseInt(__ENV.HOURLY_CYCLES            || "24");
const CALM_RPS          = parseInt(__ENV.HOURLY_CALM_RPS          || "80");
const PEAK_RPS          = parseInt(__ENV.HOURLY_PEAK_RPS          || "250");
const CHAOS_LOW_RPS     = parseInt(__ENV.HOURLY_CHAOS_LOW_RPS     || "100");
const CHAOS_HIGH_RPS    = parseInt(__ENV.HOURLY_CHAOS_HIGH_RPS    || "400");
const PLATEAU_RPS       = parseInt(__ENV.HOURLY_PLATEAU_RPS       || "280");
const WAVE_CENTER_RPS   = parseInt(__ENV.HOURLY_WAVE_CENTER_RPS   || "250");
const WAVE_AMPLITUDE    = parseInt(__ENV.HOURLY_WAVE_AMPLITUDE    || "60");
const QUIET_RPS         = parseInt(__ENV.HOURLY_QUIET_RPS         || "90");

// Phase E sinusoid: 12 one-minute stages, 5-minute period.
// stage_target(i) = CENTER + AMPLITUDE * sin(2π · i / 5).
// 12 / 5 = 2.4 full sinusoid cycles inside the 12-min phase, so the
// classifier sees several oscillation peaks per hourly cycle — useful
// CV contribution without breaking the dominant 60-min periodicity.
const WAVE_PERIOD_MIN = 5;
const WAVE_STAGES = 12;

// Phase C square wave: 100-second period per spike (8s ramp up, 42s
// high hold, 8s ramp down, 42s low hold). 6 spikes per 10-minute
// phase. Each "hold" is wider than Prometheus's 30s scrape interval
// so every spike is visible on at least one sample — the demo needs
// the chaos to be eye-catching, not subliminal.
const CHAOS_SPIKES_PER_CYCLE = 6;
const CHAOS_RAMP_SEC = 8;
const CHAOS_HOLD_SEC = 42;

// Build the full stages array. Every phase ends at the value the next
// phase begins at (or, for transitions across phases of different
// targets, the next phase's first stage uses a short snap-to duration
// followed by a long hold). This eliminates Prophet-confusing linear
// ramps that span two semantically-different phases.
function buildStages() {
  const stages = [];
  for (let c = 0; c < CYCLES; c++) {
    // Phase A (CALM, 5 min): flat at CALM_RPS. The very first cycle
    // begins at startRate=CALM_RPS (see options below) so this stage
    // is genuinely flat. Subsequent cycles ramp from QUIET_RPS=90 to
    // CALM_RPS=80 over 5 min — a 2 RPS/min descent, indistinguishable
    // from flat at the classifier's 5-min sampling resolution.
    stages.push({ target: CALM_RPS, duration: "5m" });

    // Phase B (RAMP, 10 min): single linear ramp CALM_RPS → PEAK_RPS.
    // 17 RPS/min gradient — well inside Prophet's tracking ability
    // (HOT_PATH_HISTORY_MINUTES=30 means Prophet sees ~5 samples of
    // the rising edge before the chaos phase starts).
    stages.push({ target: PEAK_RPS, duration: "10m" });

    // Phase C (CHAOS, 10 min): 6 × 100-second square-wave spikes.
    // Snap to CHAOS_LOW_RPS with a 1-second transition stage first
    // so the linear ramp from PEAK_RPS (=250) to the low rung doesn't
    // smear across the first spike's "low hold". After that snap,
    // each spike is a true square wave with sharp 8-second edges.
    stages.push({ target: CHAOS_LOW_RPS, duration: "1s" });
    for (let s = 0; s < CHAOS_SPIKES_PER_CYCLE; s++) {
      stages.push({ target: CHAOS_HIGH_RPS, duration: `${CHAOS_RAMP_SEC}s` });
      stages.push({ target: CHAOS_HIGH_RPS, duration: `${CHAOS_HOLD_SEC}s` });
      stages.push({ target: CHAOS_LOW_RPS,  duration: `${CHAOS_RAMP_SEC}s` });
      stages.push({ target: CHAOS_LOW_RPS,  duration: `${CHAOS_HOLD_SEC}s` });
    }
    // 6 × (8+42+8+42) = 600 s = 10 min ✓
    // Add a 1-second snap-to-plateau before phase D so its ramp
    // from CHAOS_LOW_RPS=100 → PLATEAU_RPS=280 doesn't smear over
    // the entire 8-min plateau (which would defeat the point).
    stages.push({ target: PLATEAU_RPS, duration: "1s" });

    // Phase D (PLATEAU, 8 min minus the C-snap and the D-snap above):
    // flat at PLATEAU_RPS. Each cycle's two 1-second snap-to stages
    // (1s before phase C, 1s before phase D) total 2s; subtracting
    // them from phase D's hold keeps the cycle on a precise 60-min
    // budget (so 24 cycles fit in exactly 24h, not 24h+24s).
    stages.push({ target: PLATEAU_RPS, duration: "7m58s" });

    // Phase E (WAVE, 12 min): discrete sinusoid. Each 1-min stage's
    // target is the sine value at the end of that minute, so k6's
    // linear interpolation between stages reconstructs a piecewise-
    // linear approximation of the sinusoid that the classifier sees
    // at its native 5-min sampling resolution as a smooth wave.
    for (let i = 1; i <= WAVE_STAGES; i++) {
      const phase = (2 * Math.PI * i) / WAVE_PERIOD_MIN;
      const target = Math.round(WAVE_CENTER_RPS + WAVE_AMPLITUDE * Math.sin(phase));
      stages.push({ target, duration: "1m" });
    }

    // Phase F (TAIL, 8 min): linear descent to QUIET_RPS. The end of
    // phase E lands at sin(2π·12/5) ≈ +0.59, i.e. ~285 RPS, so this
    // is a controlled drop of ~195 RPS over 8 min (24 RPS/min) —
    // mirrors phase B's ramp gradient on the way down.
    stages.push({ target: QUIET_RPS, duration: "8m" });

    // Phase G (QUIET, 7 min): flat at QUIET_RPS. Ends at the same
    // floor every cycle, so the start-of-next-cycle handoff into
    // phase A (CALM_RPS=80) is a 1.5 RPS/min descent — effectively
    // flat to the classifier.
    stages.push({ target: QUIET_RPS, duration: "7m" });
  }
  // Per-cycle stage total:
  //   A: 1, B: 1, C-snap: 1, C-spikes: 24, D-snap: 1, D: 1,
  //   E: 12, F: 1, G: 1  =  43 stages/cycle
  // 24 cycles = 1,032 stages — comfortably below the rotating
  // scenario's 1,590-stage profile that runs cleanly on the same
  // 2Gi-limit Pod.
  return stages;
}

export const options = {
  scenarios: {
    hourly: {
      executor: "ramping-arrival-rate",
      // startRate = CALM_RPS so cycle 1's phase A is genuinely flat.
      // Without this the first 5 min would be a ramp from 0 → 80,
      // which the classifier's first window would see as an
      // anomalous gradient_ramp pattern.
      startRate: CALM_RPS,
      timeUnit: "1s",
      // preAllocated covers CALM/QUIET comfortably (~115ms work × 90
      // RPS ≈ 11 concurrent VUs); maxVUs covers the CHAOS_HIGH_RPS
      // peak (~115ms × 400 ≈ 46 VUs) with substantial headroom.
      preAllocatedVUs: 50,
      maxVUs: 500,
      stages: buildStages(),
    },
  },
  thresholds: {
    // Maximally permissive thresholds — identical reasoning to
    // rotating.js. The demo's purpose is the forecast-accuracy +
    // 503-rate-gap comparison via Prometheus, NOT a pass/fail gate
    // on k6's own metrics. Tight thresholds (rate<0.50) risk
    // tripping on a single bad chaos cycle and turning a successful
    // 24h run into a Failed Job that's wiped after ttl.
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
