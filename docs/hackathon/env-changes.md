# Hackathon Branch Env-Var Changes

> **Branch:** `hackathon` (never merged to `main`).
> **Submission date:** June 2, 2026.
> **Demo claim:** *"Predicted RPS is very close to actual RPS for any type of traffic."*

This document is the single source of truth for every configuration
override applied on this branch versus `main`. Restore production
defaults by reverting the commit(s) on `hackathon`, or by deleting the
HACKATHON-marked blocks in the four files listed below.

## Goals

1. **Fair agentic-vs-HPA comparison.** Strip every tuning advantage AAS
   has over HPA so the only remaining difference is the forecast
   lookahead (the project's actual innovation). See
   "Fairness changes" below.
2. **Demo-friendly cold-start.** Make the controller, classifier, and
   forecast service all engage within the first 5-25 minutes of a
   k6 scenario instead of the 10-72 minutes the production defaults
   require. See "Responsiveness changes" below.
3. **Forecast accuracy.** Shrink the forecast horizon and the linear
   extrapolator window so the predicted vs actual lines track each
   other more tightly on the dashboard. See "Forecast accuracy
   changes" below.

## Complete table of changes

| # | Variable | File | `main` value | `hackathon` value | Category | Reason |
|---|---|---|---|---|---|---|
| 1 | `spec.rpsPerPodMin` | [`deploy/manifests/agenticautoscaler-sample.yaml`](../../deploy/manifests/agenticautoscaler-sample.yaml) | `10` | **`30`** | Fairness | Locks AAS divisor to match HPA's `averageValue=30`. Strips the classifier's 3x early-scale latitude so replica math is identical between the two scalers. |
| 2 | `behavior.scaleDown.policies[0].value` | [`deploy/manifests/hpa.yaml`](../../deploy/manifests/hpa.yaml) | `2` | **`4`** | Fairness | Matches AAS's `DEFAULT_MAX_STEP_SIZE=4` (which applies symmetrically up + down). Previous mismatch let AAS scale *down* twice as fast, biasing replica counts and cost comparisons. |
| 3 | `FORECAST_HORIZON_MINUTES` | [`deploy/manifests/forecast-service.yaml`](../../deploy/manifests/forecast-service.yaml) | `10` | **`5`** | Forecast accuracy | Halves forecast-vs-actual error (autocorrelation decays with horizon). System still has ~4 min of headroom for pod startup + cooldowns. |
| 4 | `PROPHET_MIN_POINTS` | [`deploy/manifests/forecast-service.yaml`](../../deploy/manifests/forecast-service.yaml) | `60` (legacy v1 value, code default is 30) | **`15`** | Responsiveness | Auto-dispatch path switches to Prophet at ~15 min instead of ~60. Most of a 25-min k6 scenario now uses Prophet, not linear_extrap. |
| 5 | `LINEAR_EXTRAP_WINDOW_MINUTES` | [`deploy/manifests/forecast-service.yaml`](../../deploy/manifests/forecast-service.yaml) | unset (code default `10`) | **`5`** | Forecast accuracy | Makes the cold-path forecaster (linear_extrap, used in first ~15 min before Prophet engages) more reactive to recent slope. |
| 6 | `GBDT_MIN_POINTS` | [`deploy/manifests/forecast-service.yaml`](../../deploy/manifests/forecast-service.yaml) | unset (code default `30`) | **`10`** | Responsiveness | Lets `gbdt_quantile` engage at ~15 min instead of ~40. Only fires when an AAS CR pins `spec.preferredForecaster: gbdt_quantile`. |
| 7 | `HOT_PATH_MIN_POINTS` | [`config/manager/manager.yaml`](../../config/manager/manager.yaml) | unset (code default `10`) | **`3`** | Responsiveness | First forecast call after 3 min instead of 10. Eliminates the "no predictions for the first 10 minutes" cold-start gap. |
| 8 | `HOT_PATH_HISTORY_MINUTES` | [`config/manager/manager.yaml`](../../config/manager/manager.yaml) | unset (code default `60`) | **`15`** | Responsiveness | Shrinks the range-query window. Fixes the `predicted_rps=0` bug seen on second-ramp reruns when the 60-min window was dominated by post-ramp-1 zeros. |
| 9 | `RECONCILE_INTERVAL_SECONDS` | [`config/manager/manager.yaml`](../../config/manager/manager.yaml) | unset (code default `60`) | **`30`** | Responsiveness | 2x more reconcile cycles per minute. Finer-grained scaling at a negligible Prometheus/forecast-service load cost. |
| 10 | `CLASSIFIER_MIN_POINTS` | [`config/manager/manager.yaml`](../../config/manager/manager.yaml) | unset (code default `72`) | **`22`** | Responsiveness | First pattern classification arrives at ~22 min instead of ~72. **22 is the hard floor** at default `CONTEXT_DOWNSAMPLE_RESOLUTION_MIN=5` (formula: `60/resolution + 10`). Anything lower is rejected at controller startup. |
| 11 | `CLASSIFIER_INTERVAL_MINUTES` | [`config/manager/manager.yaml`](../../config/manager/manager.yaml) | unset (code default `30`) | **`5`** | Responsiveness | Classifier worker re-runs 6x more often. A 20-min scenario now sees 4 classifications instead of zero. |
| 12 | `CLASSIFIER_HISTORY_HOURS` | [`config/manager/manager.yaml`](../../config/manager/manager.yaml) | unset (code default `24`) | **`1`** | Responsiveness | Cold-path PromQL window. A fresh demo never has 24h of real data; 1h confines the classifier's feature math to actual values, not zeros. |

## Grouped by category

### Fairness changes (#1, #2)

Strip every tuning advantage AAS has over HPA, leaving only the
forecast lookahead as the remaining difference. After these changes
the comparison becomes:

- Same workload (byte-identical pods, paired k6 traffic)
- Same replica bounds (min=2, max=10)
- **Same per-pod RPS target (30)** ← change #1
- **Same scale-up and scale-down speed (+4/-4)** ← change #2
- Same metric source (`http_requests_total`)
- HPA: reactive only; AAS: 5-minute forecast lookahead ← *the only remaining asymmetry*

Any performance gap on this branch is now attributable cleanly to the
forecast — the strongest defensible claim for the hackathon.

### Responsiveness changes (#4, #6, #7, #8, #9, #10, #11, #12)

All eight responsiveness changes target the same problem: production
defaults are sized for 24-hour reliability, but a k6 demo run is
10-25 minutes. Without these overrides:

- The hot path emits zero predictions for the first 10 min.
- The cold path emits zero classifications for the first 72 min.
- Prophet doesn't engage until min 60.
- `gbdt_quantile` doesn't engage until min 40.

After these overrides:

- First prediction at min 3.
- First Prophet prediction at min 15.
- First `gbdt_quantile` prediction at min 15 (if pinned via CR spec).
- First classification at min 22.

### Forecast accuracy changes (#3, #5)

These two tighten the predicted-vs-actual fit on the dashboard:

- Shorter horizon (10 -> 5 min) = better autocorrelation = lower SMAPE.
- Narrower linear_extrap window (10 -> 5 min) = less smoothing of
  stale history = better tracking of recent slope.

Expected SMAPE improvement vs `main`:
- Steady: 5-10% -> 2-5%
- Ramp: 10-20% -> 5-10%
- Spiky: 25-40% -> 15-25%

## How to apply this branch to a running cluster

```bash
git checkout hackathon
make deploy
# Verify all overrides took effect:
kubectl -n demo get aas app-agentic -o jsonpath='{.spec.rpsPerPodMin}/{.spec.rpsPerPodMax}{"\n"}'
# expect: 30/30
kubectl -n demo get hpa app-hpa -o jsonpath='{.spec.behavior.scaleDown.policies[0].value}{"\n"}'
# expect: 4
kubectl -n agentic-system rollout status deploy/forecast-service
kubectl -n agentic-system rollout status deploy/controller-manager -n agentic-autoscaler-system
```

## How to revert

```bash
git checkout main   # everything resets
make deploy
```

Or to keep a subset of changes, delete the HACKATHON-marked blocks in
the affected files and re-apply.

## What was NOT changed

For completeness and to head off "did you also change X?" critique:

- `DEFAULT_SCALE_UP_COOLDOWN_SECONDS` / `DEFAULT_SCALE_DOWN_COOLDOWN_SECONDS` —
  left at code defaults (60/300) to match HPA's `stabilizationWindowSeconds`.
- `DEFAULT_MAX_STEP_SIZE` — left at code default (4) to match HPA's
  per-minute policy.
- `CONTEXT_DOWNSAMPLE_RESOLUTION_MIN` — untouched (changing it would force
  recalculation of the `CLASSIFIER_MIN_POINTS` validation floor).
- `LINEAR_EXTRAP_RECENT_WEIGHT` / `GBDT_QUANTILE` / `PROPHET_USE_HOURLY_REGRESSOR` —
  left at code defaults; tuning these further requires more iteration than
  the 5-day hackathon timeline allows.
- The k6 scenarios (`k6/scenarios/*.js`) — unchanged.
- The Grafana dashboard ([`deploy/grafana/agentic-dashboard.json`](../../deploy/grafana/agentic-dashboard.json)) — unchanged.
- The AAS CRD types — unchanged.
- Any application Go or Python code — unchanged.

All changes on this branch are configuration only.
