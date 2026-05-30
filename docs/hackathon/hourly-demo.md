# Hourly-Cycle Demo (`hackathon-seven` branch)

> **Branch:** `hackathon-seven` (split from `hackathon-four` at `9f8aabb1`).
> **Goal:** Drive a 60-minute periodic traffic profile so the AAS
> classifier auto-selects Prophet (`PatternPeriodic`) without any
> operator pinning, then show Prophet tracking the periodicity tightly
> for the second half of a 24-hour run.

## What this demo runs

A **single k6 process** (`k6/scenarios/hourly.js`) that cycles through
seven qualitatively-distinct phases every 60 minutes for 24 hours.
Like `rotating.js` there are **zero inter-scenario gaps**: the entire
run is one `ramping-arrival-rate` executor.

| Phase | Wall-clock | Shape | RPS range |
|---|---|---|---|
| A — Calm | 0:00–5:00 | flat baseline | 80 |
| B — Ramp | 5:00–15:00 | linear climb | 80 → 250 |
| C — Chaos | 15:00–25:00 | 6× square-wave spikes (100s period: 8s ramp / 42s high / 8s ramp / 42s low) | 100 ↔ 400 |
| D — Plateau | 25:00–33:00 | flat plateau | 280 |
| E — Wave | 33:00–45:00 | 12× 1-min stages discretising a 5-min-period sinusoid | 250 ± 60 (≈190–310) |
| F — Tail | 45:00–53:00 | linear descent | 285 → 90 |
| G — Quiet | 53:00–60:00 | flat floor | 90 |

**Cycle math:** 5+10+10+8+12+8+7 = 60 min/cycle, 24 cycles = 24h. Per-cycle
totals: 43 stages, 1,032 stages over 24h.

## Why a 60-minute cycle (and not 140 like `rotating`)

The AAS classifier's hourly autocorrelation feature compares every
sample against the sample **exactly 60 minutes earlier**
(`internal/classifier/features.go::HourlyAutocorr`).

| Scenario | Cycle length | What the 60-min lag lands on | `HourlyAutocorr` | Classifier pattern | Forecaster |
|---|---|---|---|---|---|
| `rotating` | 140 min | A different phase each hour | ≈ 0 (anti-correlated) | `PatternGradualRamp` | `linear_extrap` |
| `hourly` | **60 min** | The same phase of the same cycle | ≈ **0.97** | **`PatternPeriodic`** | **`prophet`** |

This is the whole point of the scenario: keep the rest of the
hackathon-four / hackathon-seven setup intact, but make the cycle
length match the autocorrelation lag so the classifier's auto-dispatch
path picks Prophet on its own. **No `preferredForecaster: prophet` pin
is required**; in fact this branch deliberately un-pins it (commit
`47cadcb2`) to demonstrate the auto-selection working.

## Forecaster timeline across a 24h run

Internal/classifier needs `MinPoints=72` samples at 5-min resolution
before it produces a verdict — that's 6 hours of history. Before then
the controller falls back to `linear_extrap`. After then, Prophet
itself needs a warm-up period before its `hourly_profile` regressor
engages (`HOURLY_PROFILE_MIN_HOURS=12`).

| Wall-clock | Active forecaster | Why |
|---|---|---|
| 0–6 h | `linear_extrap` | Classifier hasn't met `MinPoints=72`; auto-dispatch falls back to linear. |
| 6–12 h | `prophet` (partial) | Classifier engages, sees ≥1 full 60-min cycle, scores `HourlyAutocorr≈0.97` → `PatternPeriodic` → `prophet`. Prophet uses trend + changepoint detection only. |
| 12–24 h | **`prophet` (full hourly profile)** | Prophet's `hour_baseline` regressor has 12 hours of (hour-of-day, RPS) pairs and slots into the prediction. Predicted line tracks every phase boundary within ~30 s. |

The 12-hour mark is where the demo recording should focus. Before
then it looks like a slightly-late linear chaser; from minute 720
onwards the predicted line visibly anticipates each phase transition
by ~5 minutes (the `FORECAST_HORIZON_MINUTES=5` window).

## Running

```bash
git checkout hackathon-seven
make deploy
make k6-incluster-hourly
```

The wrapper is set up for long-run survivability (signals are
trapped to a *detach* rather than a *delete*, like `diurnal` /
`rotating`). Launch under `tmux` or `nohup` so a terminal close
doesn't stop the run:

```bash
tmux new -s k6 "make k6-incluster-hourly"
# OR
nohup make k6-incluster-hourly > k6-hourly.log 2>&1 &
```

To compress for a smoke test (one full cycle = 60 min, or two = 2h):

```bash
HOURLY_CYCLES=1 make k6-incluster-hourly   # 1h, validates cycle shape
HOURLY_CYCLES=2 make k6-incluster-hourly   # 2h, validates handoff
```

**Note:** with `HOURLY_CYCLES=1` you'll never see Prophet engage —
the classifier needs 6h of history. Anything below 12h is testing
the *traffic shape*, not the forecaster claim. For the full
prediction-quality story, run all 24 cycles.

## Tunables

| Env var | Default | What it controls |
|---|---|---|
| `HOURLY_CYCLES` | `24` | Number of 60-min cycles (24 = 24 h). |
| `HOURLY_CALM_RPS` | `80` | Phase A baseline. |
| `HOURLY_PEAK_RPS` | `250` | Phase B ramp target. |
| `HOURLY_CHAOS_LOW_RPS` | `100` | Phase C low rung of the square wave. |
| `HOURLY_CHAOS_HIGH_RPS` | `400` | Phase C high rung (and the demo's overall RPS peak). |
| `HOURLY_PLATEAU_RPS` | `280` | Phase D flat plateau. |
| `HOURLY_WAVE_CENTER_RPS` | `250` | Phase E sinusoid centre. |
| `HOURLY_WAVE_AMPLITUDE` | `60` | Phase E sinusoid peak-to-trough amplitude. |
| `HOURLY_QUIET_RPS` | `90` | Phase F descent target and phase G floor. |

The 60-minute cycle length is **not** a tunable — it's deliberately
hard-coded to match the classifier's 60-minute autocorrelation lag.
Rescaling the phase RPS values is fine; reshaping the cycle into a
different period defeats the auto-selection guarantee.

## Why this differs from `rotating` (the other 24h scenario)

| Concern | `hackathon-two`-era `rotating` | `hackathon-seven` `hourly` |
|---|---|---|
| Cycle length | 140 min | **60 min** (matches autocorr lag) |
| Auto-dispatch verdict | `PatternGradualRamp` → `linear_extrap` | **`PatternPeriodic` → `prophet`** |
| Requires `preferredForecaster: prophet` pin? | Yes (set in CR) | **No** (un-pinned; classifier picks) |
| Phase variety | 4 shapes (steady / ramp / spiky / bursty) | 7 shapes (calm / ramp / chaos / plateau / wave / tail / quiet) |
| RPS range | 60–220 | 80–400 |
| Stages per cycle | 159 | 43 |
| Demo claim it supports | "predicted ≈ actual across rotating patterns *when Prophet is pinned*" | **"the classifier auto-selects Prophet on its own when the workload is periodic, then Prophet tracks every phase"** |

The narrower stages-per-cycle count is the only place this scenario
costs less than `rotating`; everywhere else the trade-off is in
favour of richer waveforms — including a square-wave chaos phase
that no previous scenario has (the bursty phase in rotating is
LCG-noise, not a deterministic square wave).

## What changed on `hackathon-seven` vs `hackathon-four`

Three configuration deltas (already on this branch from earlier
commits) plus this scenario:

| # | Change | File | `hackathon-four` | `hackathon-seven` | Why |
|---|---|---|---|---|---|
| H7-1 | Equalise per-pod target | [`deploy/manifests/hpa.yaml`](../../deploy/manifests/hpa.yaml) | `averageValue: 50` | **`40`** | Was a fairness fix — both scalers now divide RPS by 40. The previous 50/30 split silently buried AAS's 33%-headroom advantage inside an arithmetic asymmetry, not the forecast. |
| H7-1 | Equalise per-pod target | [`deploy/manifests/agenticautoscaler-sample.yaml`](../../deploy/manifests/agenticautoscaler-sample.yaml) | `rpsPerPodMin/Max: 30/31` | **`40/41`** | Same fix, AAS side. Both controllers now scale identically against any given RPS; the only remaining difference is the 5-min forecast lookahead. |
| H7-2 | Drop adapter 5xx filter | [`deploy/helm/prometheus-adapter-values.yaml`](../../deploy/helm/prometheus-adapter-values.yaml) | `status=~"2.."` filter active | **filter removed** | Was a hidden HPA handicap on hackathon-four: 5xx responses were excluded from the per-pod RPS metric, so under-capacity HPA pods looked *less loaded* the worse they got — a death-spiral that prevented HPA scaling up. Cherry-picked from `hackathon-six` (commit `d0f3e017`). |
| H7-3 | Un-pin AAS forecaster | [`deploy/manifests/agenticautoscaler-sample.yaml`](../../deploy/manifests/agenticautoscaler-sample.yaml) | `preferredForecaster: prophet` | **(commented out → `auto`)** | Lets the classifier pick. Used to be a pin because the `rotating` scenario's 140-min cycle can't earn the `PatternPeriodic` verdict. With the new `hourly` scenario the verdict comes for free, and showing auto-selection is a stronger demo than showing the operator-pinned variant. |
| H7-4 | Add `hourly` scenario | [`k6/scenarios/hourly.js`](../../k6/scenarios/hourly.js) | n/a | **new** | This file. Designed to be the only scenario where the operator can leave AAS at `preferredForecaster: auto` and still get Prophet. |

## What to watch on Grafana

The recording should focus on the **last 12 hours** of a 24h run.
By then:

1. **Classifier pattern** stays at `PatternPeriodic` (visible in
   `kubectl get aas app-agentic -o jsonpath='{.status.classifiedParams.pattern}'`).
2. **Active forecaster** stays at `prophet` (no flapping).
3. **Predicted vs actual** lines on the Grafana dashboard track
   each phase boundary within ~30 s.

**Slide-friendly moments (hours 12–24):**

- **Phase A → B transition** (minute 5 of each hour): predicted line
  begins climbing ~5 min before actual does. HPA only reacts once
  actual has already moved.
- **Phase B → C transition** (minute 15): the predicted line jumps
  to a higher mean as Prophet's hourly profile knows the chaos
  phase is about to start. The square-wave spikes themselves are
  too fast for Prophet to learn individually, but predicted
  oscillates around the (chaos-aware) mean rather than the
  pre-chaos plateau.
- **Phase E → F transition** (minute 45): predicted descent line
  starts ~5 min before actual peels off the sinusoid. The cleanest
  visual demonstration of "AAS scaled down preemptively while HPA
  is still serving the wave peak".

Expected metrics by the end of hour 24:

| Metric | HPA | AAS | Improvement |
|---|---|---|---|
| Total 503 count | High during every B and C transition | Near zero during transitions | 5–10× lower for AAS |
| Mean replica count | Reactive, lags traffic | Anticipatory, slight lead | similar (the forecast is mostly used for *timing*, not magnitude) |
| Phase-boundary p99 latency | Spikes at every transition | Stable | tightest at the B/C boundary |

## Verify the deploy

```bash
kubectl -n demo get aas app-agentic -o jsonpath='{.spec.preferredForecaster}{"\n"}'
# expect: "" (empty → auto-dispatch)

kubectl -n demo get aas app-agentic -o jsonpath='{.spec.rpsPerPodMin}/{.spec.rpsPerPodMax}{"\n"}'
# expect: 40/41

kubectl -n demo get hpa app-hpa -o jsonpath='{.spec.metrics[0].pods.target.averageValue}{"\n"}'
# expect: 40

kubectl -n monitoring get cm prometheus-adapter -o yaml \
  | grep -A1 metricsQuery | grep -v 'status=~"2..'
# expect: a metricsQuery line WITHOUT the status filter (one line of output)
```

Once the `hourly` Job has been running for ≥6 hours:

```bash
kubectl -n demo get aas app-agentic -o jsonpath='{.status.classifiedParams.pattern}{"\n"}'
# expect (after 6h):  periodic
kubectl -n demo get aas app-agentic -o jsonpath='{.status.classifiedParams.preferredForecaster}{"\n"}'
# expect (after 6h):  prophet
```

If the pattern is still `gradual_ramp` after 6 hours, check that
`HOURLY_CYCLES` wasn't overridden to a value smaller than the elapsed
hours and that no other k6 Job is co-running and polluting the
RPS signal.

## How to revert

```bash
git checkout hackathon-four   # back to the demo-tuned config (no auto-Prophet)
make deploy
```

Or, to keep the scenario but go back to pinned Prophet:

```bash
git checkout hackathon-seven
git revert 47cadcb2           # restores preferredForecaster: prophet
make deploy
```
