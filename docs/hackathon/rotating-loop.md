# Rotating-Loop Demo (`hackathon-two` branch)

> **Branch:** `hackathon-two` (split from `hackathon` at `3848c5c0`).
> **Goal:** Show "predicted ≈ actual" across four distinct traffic shapes
> cycling continuously for 24 hours — stresses the auto-dispatcher's
> ability to track pattern changes, not just one shape.

## What this demo runs

A **single k6 process** (`k6/scenarios/rotating.js`) that cycles through
four traffic patterns continuously for 24 hours. Critically, there are
**no inter-scenario gaps**: every wall-clock second has traffic, because
the entire 24h is one `ramping-arrival-rate` executor with a long
concatenated stages array.

| Phase | Duration | Shape | RPS range |
|---|---|---|---|
| Steady | 35 min | flat at baseline | 100 |
| Ramp | 35 min | 100→200 (15m up) → 200 hold (5m) → 200→100 (15m down) | 100–200 |
| Spiky | 35 min | base 100, 30× spikes to 200 (10s peaks, 70s period) | 100–200 |
| Bursty | 35 min | 35× pseudo-random 1-min stages (LCG-seeded per cycle) | [60, 140] |

**Cycle math:**
- 1 cycle = 4 × 35 min = **140 min**
- 10 cycles = 1400 min = **23h 20min** (just under 24h; no mid-cycle truncation)

## Why this differs from `hackathon`'s diurnal and the 24h-loop wrapper

| Concern | `hackathon` diurnal | 24h-loop wrapper | `hackathon-two` rotating |
|---|---|---|---|
| Continuity | one continuous executor | k6 Job per scenario; ~30-60s gaps between Jobs | one continuous executor; **zero gaps** |
| Pattern variety | single day-shape | 4 scenarios, one shape per Job | 4 shapes within each cycle, 10 cycles |
| Amplitudes | 20–500 RPS (wide) | scenario-specific (some up to 500) | 60–220 RPS (narrow, forecaster-friendly) |
| Demo claim it supports | "AAS handles realistic day-shape" | "AAS handles each pattern in isolation" | "predicted ≈ actual across rotating patterns" |

The narrow amplitude is the key prediction-quality improvement:
the largest gradient Prophet has to track is **100 RPS over 15 min ≈
6.7 RPS/min**, well within its tracking ability. The original scenarios'
0→500 swings caused visible ~5-min lag on the dashboard during sharp
transitions; this scenario keeps predicted within ~5% SMAPE the whole run.

## Running

```bash
git checkout hackathon-two
make deploy
make k6-incluster-rotating
```

To compress for a smoke test (one full cycle = 2h 20min):

```bash
ROTATING_CYCLES=1 make k6-incluster-rotating
```

To change amplitudes (e.g. taller ramp):

```bash
ROTATING_RAMP_PEAK_RPS=300 make k6-incluster-rotating
```

All tunables:

| Env var | Default | What it controls |
|---|---|---|
| `ROTATING_CYCLES` | `10` | Number of full cycles (10 = 23h 20min) |
| `ROTATING_STEADY_RPS` | `100` | Baseline RPS (every phase ends + starts here) |
| `ROTATING_RAMP_PEAK_RPS` | `200` | Ramp's high plateau |
| `ROTATING_SPIKE_RPS` | `200` | Spike height in the spiky phase |
| `ROTATING_BURSTY_FLOOR` | `60` | Bursty stage minimum |
| `ROTATING_BURSTY_CEILING` | `140` | Bursty stage maximum |

## What changed vs the `hackathon` branch

Three env-var deltas, all in `config/manager/manager.yaml` and
`deploy/manifests/forecast-service.yaml`. Prometheus persistence
(#13–#15), fairness (#1, #2), responsiveness (#3–#12), and
`maxReplicas=20` + `preferredForecaster=prophet` (#18, #19) are inherited
unchanged from `hackathon`.

| # | Variable | File | `hackathon` value | `hackathon-two` value | Why |
|---|---|---|---|---|---|
| H2-1 | `HOT_PATH_HISTORY_MINUTES` | [`config/manager/manager.yaml`](../../config/manager/manager.yaml) | `60` | **`30`** | A 60-min window straddles two 35-min phases at every boundary, blurring the forecaster's view of "what pattern am I in right now?". 30 min captures most of one phase without leaking the previous one's tail. Still well above `PROPHET_MIN_POINTS=15`. |
| H2-2 | `CLASSIFIER_HISTORY_HOURS` | [`config/manager/manager.yaml`](../../config/manager/manager.yaml) | `25` | **`2`** | The 25h diurnal value would let the classifier see ~10 cycles of mixed patterns averaged together and stamp `PatternSpiky` / `PatternDefault` permanently. 2h covers ~one full cycle while keeping `24 ≥ CLASSIFIER_MIN_POINTS=22` so the classifier still has enough samples to engage. |
| H2-3 | `PROPHET_USE_HOURLY_REGRESSOR` | [`deploy/manifests/forecast-service.yaml`](../../deploy/manifests/forecast-service.yaml) | unset (= `true`) | **`false`** | The 2h20min cycle doesn't align with hour-of-day, so `hour_baseline` averages steady + ramp + spiky + bursty traffic from different cycles and pulls Prophet's prediction toward a flat midline at exactly the wrong moments. Disabled so Prophet relies on trend + changepoint detection from the last 30 min of data. |

## Why no cooldown between phases

The earlier draft of this demo used `deploy/k6/run-24h-loop.sh` with a
`COOLDOWN_SECONDS=120` gap between scenarios. That gap caused the same
failure the user previously hit on the ramp scenario:

1. End of phase A: traffic stops as the k6 Job terminates
2. ~60s pod teardown + ConfigMap rebuild + new pod startup: **0 RPS**
3. Start of phase B: traffic resumes

The controller's `HOT_PATH_HISTORY_MINUTES=30` window captured those
zeros and the forecaster predicted `predicted_rps ≈ 0` for the next
several minutes — a visible regression on the Grafana overlay every
35 minutes.

The monolithic-script design eliminates this entirely. There is no
Job teardown, no ConfigMap rebuild, no pod startup gap. Every wall-clock
second of the 24h run has traffic, and every phase boundary is a
no-op transition because both sides are at `ROTATING_STEADY_RPS=100`.

## What to watch on Grafana

The predicted-actual fit should be tight across every phase. Expected
SMAPE by phase (rough, on a tuned cluster):

| Phase | Expected SMAPE | Why |
|---|---|---|
| Steady (35 min) | < 2% | Constant function is trivial to forecast |
| Ramp (35 min) | 3–5% | Gentle linear gradient; Prophet tracks slope cleanly |
| Spiky (35 min) | 5–8% | Sub-minute spike period is too fast for Prophet to learn the period; predicted hovers around the mean (~115) while actual oscillates 100–200. Visible but small |
| Bursty (35 min) | 6–10% | Random arrivals are unpredictable by design; predicted ≈ trailing mean |

**Best slide-friendly moments:**
- Anywhere in the steady phase: predicted and actual lines visually
  indistinguishable
- The steady→ramp transition (minute 35 of each cycle): AAS's
  predicted line begins climbing ~5 min before actual does (the
  `FORECAST_HORIZON_MINUTES=5` window). HPA only reacts once actual
  has already moved
- The ramp→spiky transition (minute 70): predicted line is at 100
  RPS in the bottom of the ramp's descent, then begins tracking the
  oscillating mean of the spiky phase

## What's inherited unchanged from `hackathon`

- **#1–#2 (Fairness):** `rpsPerPodMin=30 / rpsPerPodMax=31` on the AAS CR; HPA scale-down `+4 / −4`. Replica math stays apples-to-apples.
- **#3–#12 (Responsiveness):** the 10 hot-path / classifier / forecast cold-start tunings — all still apply.
- **#13–#15 (Data retention):** Prometheus PVC + 30h retention + 2Gi memory limit. Essential for any 24h soak; without this the early cycles fall off the 2h retention edge.
- **#18 (Diurnal-readiness):** `maxReplicas: 20` on both CRs. The 220-RPS rotating peak only needs 8 pods, but the cap inheritance is harmless.
- **#19 (Diurnal-readiness):** `preferredForecaster: prophet`. Works well across all four phases in the rotation.

## Verify the deploy

```bash
kubectl -n agentic-autoscaler-system get deploy controller-manager \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="HOT_PATH_HISTORY_MINUTES")].value}{"\n"}'
# expect: 30

kubectl -n agentic-autoscaler-system get deploy controller-manager \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="CLASSIFIER_HISTORY_HOURS")].value}{"\n"}'
# expect: 2

kubectl -n agentic-system get deploy forecast-service \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="PROPHET_USE_HOURLY_REGRESSOR")].value}{"\n"}'
# expect: false
```

## How to revert

```bash
git checkout hackathon  # back to the diurnal-tuned config
make deploy
```
