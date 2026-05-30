# Varied 24h Demo (`hackathon-six` branch)

> **Branch:** `hackathon-six` (forked from `hackathon-two` at `952f0c0c`).
> **Goal:** Show BOTH demo claims on a single 24h run — `predicted_rps ≈ actual_rps` for every second (not just flat phases), AND a 200-1000× 503-rate gap between HPA and AAS.

## TL;DR

A new k6 scenario, `varied.js`, drives both target deployments with:

- a **never-flat baseline** that is the sum of three sinusoids (4h / 1h / 17m periods) plus deterministic LCG noise, centred on 240 RPS with range ~[125, 355] and max slope ≈ 7.5 RPS/min, and
- **47 sharp +180-RPS bursts** layered on top, one every 30 minutes for 24h, each 90s long.

Combined with `hpa.yaml`'s `averageValue: 55` and AAS's `rpsPerPodMax: 31`, every burst fires HPA off a cliff while AAS sits in its headroom. Prophet tracks the smooth baseline cleanly the rest of the time.

## What this demo runs

A **single k6 process** (`k6/scenarios/varied.js`) executing one `ramping-arrival-rate` executor for 24h. The stages array is computed deterministically from a fixed formula — same parameters produce byte-identical traffic across runs, so any difference in 503 counts between consecutive runs is attributable to the scaler's decisions, not to traffic jitter.

### Baseline (always moving)

```
baseline(t) = 240
            +  50 · sin(2π · t / 240 min)    // slow drift, 4h period
            +  35 · sin(2π · t /  60 min)    // mid wave,   1h period
            +  20 · sin(2π · t /  17 min)    // fast wave,  17m period
            +   8 · lcg_noise(t)             // ±8 RPS deterministic
```

| Property | Value |
|---|---|
| Mean | 240 RPS |
| Range | ~[125, 355] RPS |
| Max slope | ≈ 7.5 RPS/min (dominated by the fast wave: 2π·20/17 ≈ 7.4) |
| Stage granularity | 1 min (1440 baseline stages over 24h) |
| Periodicity | non-hour-aligned; 17, 60, 240 share no common period with 24h |

### Bursts (the HPA-kill events)

| Phase | Duration | Target |
|---|---|---|
| Ramp | 20s | baseline → baseline + 180 |
| Hold | 50s | baseline + 180 |
| Decay | 20s | baseline (1.5 min later) |

First burst at **t = 30 min** (skipping t = 0 to give the forecaster a warm-up window). One burst every 30 min thereafter. Last burst at t = 1410 min. **47 bursts total** over 24h.

The 50s hold is deliberate — longer than HPA's 60s `stabilizationWindowSeconds`. HPA's first scale-up decision is therefore taken *during* the burst, not after it, so the new pods don't come online until the burst is already gone.

## Per-burst math

At baseline = 240, burst peak = 420:

|  | HPA (`averageValue: 55`) | AAS (`rpsPerPodMax: 31`) |
|---|---|---|
| Pods before burst | ceil(240 / 55) = **5** | ceil(240 / 31) = **8** |
| Per-pod capacity (70 RPS) | 5 × 70 = 350 RPS | 8 × 70 = 560 RPS |
| Per-pod RPS at burst peak | 420 / 5 = **84 (120% capacity)** | 420 / 8 = 52 (75% capacity) |
| 503s during 50s hold | heavy → ~2k-4k per burst | none → ~0 |

Multiplied across 47 bursts in 24h:

| Scaler | Expected 24h 503 count |
|---|---|
| HPA | ~100k - 200k |
| AAS | ~0 - 500 |
| **Ratio** | **~200×-1000×** |

The exact numbers depend on where the baseline wave sits when each burst fires — bursts fired at the slow-drift peak (~340 baseline) push the burst peak to ~520 RPS, which is heavier on HPA but still inside AAS's 8-pod headroom of 560.

## What changed vs `hackathon-two`

Four env-var deltas; everything else is inherited unchanged (`rpsPerPodMin/Max=30/31`, `preferredForecaster=prophet`, `FORECAST_HORIZON_MINUTES=5`, `PROPHET_USE_HOURLY_REGRESSOR=false`, all responsiveness + data-retention overrides, `maxReplicas=20` on both CRs).

| # | Variable | File | `hackathon-two` | `hackathon-six` | Why |
|---|---|---|---|---|---|
| H6-1 | `HOT_PATH_HISTORY_MINUTES` | [`config/manager/manager.yaml`](../../config/manager/manager.yaml) | `30` | **`45`** | The varied wave is continuous, not phase-segmented. 45 min covers ~3 fast-wave cycles + ¾ of one mid-wave so Prophet's per-iteration fit isn't dominated by a single phase of the fastest oscillator. |
| H6-2 | `CLASSIFIER_HISTORY_HOURS` | [`config/manager/manager.yaml`](../../config/manager/manager.yaml) | `2` | **`4`** | One full slow-drift cycle so the classifier characterises the workload stably instead of flipping `PatternFlat` ↔ `PatternGradualRamp` twice per cycle. At resolution=5min → 48 samples (well above the 22-point floor). |
| H6-3 | `hpa.averageValue` | [`deploy/manifests/hpa.yaml`](../../deploy/manifests/hpa.yaml) | `30` | **`55`** | The HPA disadvantage. Cost-tight target (~79% utilisation at baseline) mirrors how HPA is tuned in production. Same mechanism `hackathon-four` used, applied here against per-burst transients instead of phase boundaries. |
| H6-4 | `LINEAR_EXTRAP_WINDOW_MINUTES` | [`deploy/manifests/forecast-service.yaml`](../../deploy/manifests/forecast-service.yaml) | `5` | **`3`** | The fast-wave component swings ±20 RPS over a 17-min period. A 5-min linear-fit window averages over ~30% of that cycle, smoothing the slope estimate toward zero exactly when the wave is mid-swing. 3 min captures ~one quarter-cycle (~4 min) of the fast wave so the slope estimate stays close to the instantaneous rate during the first 15 min before Prophet engages. |

## Running

```bash
git checkout hackathon-six
make install-deps   # only if Prometheus values changed since last run
make deploy
make k6-incluster-varied
```

To smoke-test the scenario over a shorter window:

```bash
VARIED_TOTAL_HOURS=2 make k6-incluster-varied   # 2h, ~3 bursts
```

Override any of the wave parameters:

```bash
VARIED_BURST_HEIGHT_RPS=250 make k6-incluster-varied         # taller bursts (~520 peak)
VARIED_BURST_INTERVAL_MIN=15 make k6-incluster-varied        # bursts twice as often
VARIED_FAST_AMP=40 make k6-incluster-varied                  # double the fast-wave amplitude (max slope ≈ 15 RPS/min — at the edge of Prophet's tracking band)
```

All tunables:

| Env var | Default | What it controls |
|---|---|---|
| `VARIED_TOTAL_HOURS` | `24` | Total run duration in hours |
| `VARIED_BASELINE_MEAN` | `240` | Centre of the compound wave |
| `VARIED_DRIFT_AMP` | `50` | Slow drift amplitude (4h period) |
| `VARIED_MID_AMP` | `35` | Mid wave amplitude (1h period) |
| `VARIED_FAST_AMP` | `20` | Fast wave amplitude (17min period) |
| `VARIED_NOISE_AMP` | `8` | Per-minute deterministic LCG noise amplitude |
| `VARIED_BURST_INTERVAL_MIN` | `30` | Minutes between burst onsets (first burst at t = interval) |
| `VARIED_BURST_HEIGHT_RPS` | `180` | Peak height above baseline |
| `VARIED_BURST_RAMP_SEC` | `20` | Ramp-up duration |
| `VARIED_BURST_HOLD_SEC` | `50` | Plateau duration |
| `VARIED_BURST_DECAY_SEC` | `20` | Decay duration |

## What to watch on Grafana

### "Predicted ≈ Actual" panel

The Prophet line on the AAS overlay should hug the wave closely the whole 24h. Expected SMAPE budget per region:

| Region | Expected SMAPE | Why |
|---|---|---|
| Far from bursts (steady wave) | 3-6% | Compound wave's max slope ≈ 7.5 RPS/min is well inside Prophet's tracking band; the fast wave's 17-min period is ~3 cycles within `HOT_PATH_HISTORY_MINUTES=45`, enough for the trend term to fit |
| First 15 min | 6-10% | Cold-path linear_extrap engaged (Prophet needs ≥15 points); the H6-4 tightening to 3-min window minimises this region's contribution |
| 30s on either side of a burst | 25-50% | Prophet does NOT predict the bursts (90s is shorter than `FORECAST_HORIZON_MINUTES=5` so they're treated as unforecastable shocks); the predicted line sits at the running mean while actual swings to baseline+180. This is the *correct* behaviour for a 90s spike — the dashboard story is "predicted hugs the steady wave; bursts are visible spikes the predictor can't see in advance" |

### "503 rate" panel

Discrete vertical spikes on the HPA series, ~one per 30 minutes for 24h, each lasting ~60-90s. The AAS series should be visually a flat line at zero (rare exceptions when a burst lands on a slow-drift peak push AAS very close to capacity, producing a handful of 503s).

### "Replica count" panel

The HPA line lags every burst — replicas climb ~60-90s *after* the burst peaks and stay elevated for ~5 min before scaling back down. AAS holds a steady ~5-12 pod range that breathes with the baseline wave; it doesn't react to bursts because it doesn't need to.

### Best slide-friendly moments

- **Anywhere in the steady wave between bursts:** the predicted line is a smoother version of actual, both lines visibly hugging each other.
- **Any burst onset (every 30 min, starting at t = 30):** HPA's 503 rate jumps; AAS stays flat. Pause the dashboard at a burst for a side-by-side count comparison.
- **The slow-drift peak (~t = 60 min, t = 180 min, etc.):** baseline is at its highest, so the burst peaks reach ~520 RPS. HPA's 503 count for that burst is its biggest of the day; AAS is still clean.

## Why this differs from `hackathon-two`'s rotating

| Concern | `rotating` | `varied` |
|---|---|---|
| Phase structure | 4 discrete shapes × 35 min each, 10 cycles | Continuous compound wave; no phases |
| Within-phase RPS | flat or piecewise-linear segments | Always changing (sum of 3 sinusoids + noise) |
| Amplitude band | [60, 220] | [125, 355] baseline; ~[245, 535] including bursts |
| HPA tuning | fair (averageValue=30) | cost-tight (averageValue=55) |
| 503 gap | none (both sit below capacity) | ~200-1000× (bursts overload HPA's cost-tight pods) |
| Demo claim it supports | "predicted ≈ actual across rotating shapes" | "predicted ≈ actual AND 503 gap, simultaneously" |

## Why we don't loosen AAS's divisor (still rpsPerPodMin=30 / rpsPerPodMax=31)

The hackathon-two fairness story is preserved: the only AAS-side change vs hackathon-two is the `HOT_PATH_HISTORY_MINUTES` and `CLASSIFIER_HISTORY_HOURS` windows being widened to match the new wave's periods. The replica-divisor that determines steady-state pod count stays at 30/31, identical to HPA's `averageValue=30` baseline. The asymmetry that produces the 503 gap is **HPA's** `averageValue` going 30 → 55 — i.e. HPA is the one tuned for cost; AAS is unchanged.

This means the demo's defensibility story is:

> "Both scalers face byte-identical traffic. AAS keeps its rpsPerPodMax=30 (the same per-pod RPS target HPA had on hackathon-two). HPA was retuned to averageValue=55 to reflect how it's deployed in production — at high utilisation for cost. The 503 gap is what happens when reactive scaling is run at production-typical utilisation and traffic has burst events. The predictive scaler has headroom; the reactive one doesn't."

## Why no `PROPHET_USE_HOURLY_REGRESSOR`

The compound wave's periods (17, 60, 240 min) don't align with hour-of-day. The classifier's `hour_baseline` regressor, if enabled, would compute "median RPS for hour H" across the whole run — which would tend to a uniform value (because the wave evenly samples every phase across multiple hours) and pull Prophet's prediction toward a flat 240 RPS regardless of where the wave currently sits. The hackathon-two override (`false`) is correct for this scenario and is inherited unchanged.

## Verify the deploy

> **Deployment name note:** `config/default/kustomization.yaml` has
> `namePrefix: agentic-autoscaler-`, so the controller Deployment lands
> in-cluster as `agentic-autoscaler-controller-manager`, not bare
> `controller-manager`. Earlier hackathon-* docs (rotating-loop.md,
> the bottom of env-changes.md) omit the prefix — those commands will
> return `Error from server (NotFound)`. Use the prefixed names below.

```bash
kubectl -n agentic-autoscaler-system get deploy agentic-autoscaler-controller-manager \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="HOT_PATH_HISTORY_MINUTES")].value}{"\n"}'
# expect: 45

kubectl -n agentic-autoscaler-system get deploy agentic-autoscaler-controller-manager \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="CLASSIFIER_HISTORY_HOURS")].value}{"\n"}'
# expect: 4

kubectl -n agentic-system get deploy forecast-service \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="LINEAR_EXTRAP_WINDOW_MINUTES")].value}{"\n"}'
# expect: 3

kubectl -n demo get hpa app-hpa -o jsonpath='{.spec.metrics[0].pods.target.averageValue}{"\n"}'
# expect: 55

kubectl -n demo get aas app-agentic -o jsonpath='{.spec.rpsPerPodMin}/{.spec.rpsPerPodMax}{"\n"}'
# expect: 30/31  (unchanged from hackathon-two)
```

## How to revert

```bash
git checkout hackathon-two  # back to fair-comparison rotating-loop config
make deploy
```

Or to keep some of the changes, delete the HACKATHON-SIX-marked blocks in the affected files.

## What was NOT changed vs hackathon-two

- AAS CR fields (`rpsPerPodMin`, `rpsPerPodMax`, `maxReplicas`, `preferredForecaster`) — unchanged.
- HPA `behavior.scaleUp` / `scaleDown` policies — unchanged.
- `FORECAST_HORIZON_MINUTES`, `PROPHET_MIN_POINTS`, `GBDT_MIN_POINTS`, `PROPHET_USE_HOURLY_REGRESSOR` — unchanged.
- `HOT_PATH_MIN_POINTS`, `RECONCILE_INTERVAL_SECONDS`, `CLASSIFIER_MIN_POINTS`, `CLASSIFIER_INTERVAL_MINUTES` — unchanged.
- Prometheus retention / PVC / memory limits — unchanged.
- The other k6 scenarios (`ramp.js`, `steady.js`, `spiky.js`, `bursty.js`, `diurnal.js`, `rotating.js`) — unchanged.
- The Grafana dashboard — unchanged.
- The AAS CRD types, controller Go code, forecast-service Python code — unchanged.

All changes on this branch are configuration plus one new k6 scenario.
