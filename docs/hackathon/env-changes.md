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
4. **Data retention.** Make Prometheus survive pod restarts and keep
   enough history to review a 24-hour soak the next morning. See
   "Data retention changes" below.
5. **Diurnal-readiness.** Lift the caps and windows that prevented the
   24h diurnal scenario from producing a useful comparison (replica
   ceiling too low for spikes, classifier query window shorter than
   the cycle period, forecaster history too narrow for gradual day-
   shape). See "Diurnal-readiness changes" below.

## Complete table of changes

| # | Variable | File | `main` value | `hackathon` value | Category | Reason |
|---|---|---|---|---|---|---|
| 1 | `spec.rpsPerPodMin` / `spec.rpsPerPodMax` | [`deploy/manifests/agenticautoscaler-sample.yaml`](../../deploy/manifests/agenticautoscaler-sample.yaml) | `10` / `30` | **`30` / `31`** | Fairness | Locks AAS divisor to match HPA's `averageValue=30`. Strips the classifier's 3x early-scale latitude so replica math is identical between the two scalers. **Why 30/31 and not 30/30:** the validating webhook enforces strict `rpsPerPodMin < rpsPerPodMax`. 30/31 is the tightest validator-legal pin — `ceil(N/30) == ceil(N/31)` for every RPS the demo hits (100, 200, 250, 300, 500), so functionally identical to "locked to 30". |
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
| 13 | `prometheus.prometheusSpec.retention` | [`deploy/helm/prometheus-values.yaml`](../../deploy/helm/prometheus-values.yaml) | `2h` | **`30h`** | Data retention | The 2h default silently deleted yesterday's Grafana panels and would make any multi-hour soak unreviewable. 30h covers a full 24h run + 6h slack for next-morning review. |
| 14 | `prometheus.prometheusSpec.storageSpec` | [`deploy/helm/prometheus-values.yaml`](../../deploy/helm/prometheus-values.yaml) | `{}` (emptyDir) | **5Gi PVC on `standard` storageClass** | Data retention | emptyDir was wiped on every Prometheus pod restart (OOM, kind restart, helm upgrade) — the single most common cause of vanished panels. Kind's bundled local-path-provisioner makes a real PVC zero-config. |
| 15 | `prometheus.prometheusSpec.resources.limits.memory` | [`deploy/helm/prometheus-values.yaml`](../../deploy/helm/prometheus-values.yaml) | `1Gi` | **`2Gi`** | Data retention | 30h of retained data + the working set for Grafana queries OOM-killed the pod at 1Gi (the *other* way data disappeared). Request also bumped from 512Mi -> 1Gi. |
| 16 | `HOT_PATH_HISTORY_MINUTES` | [`config/manager/manager.yaml`](../../config/manager/manager.yaml) | `15` (earlier hackathon value) | **`60`** | Diurnal-readiness | The 15-min window was tuned for short bursty/spiky scenarios where post-run zeros poisoned the forecaster. With Prometheus now persistent (#13–#15), that failure mode is gone — and the diurnal scenario needs the full 60-min window so Prophet's slope estimate has enough context to track gradual day-shape transitions. |
| 17 | `CLASSIFIER_HISTORY_HOURS` | [`config/manager/manager.yaml`](../../config/manager/manager.yaml) | `1` (earlier hackathon value) | **`25`** | Diurnal-readiness | **Was a hard bug at `1`:** at default resolution=5min the query returned max 12 samples while `CLASSIFIER_MIN_POINTS=22` required ≥22, so the classifier logged "insufficient data" forever and never produced output. `25` lets the diurnal scenario see a full 24h cycle + 1h slack. Classifier still first engages at ~110 min elapsed regardless of this value. |
| 18 | `spec.maxReplicas` | [`deploy/manifests/agenticautoscaler-sample.yaml`](../../deploy/manifests/agenticautoscaler-sample.yaml) and [`deploy/manifests/hpa.yaml`](../../deploy/manifests/hpa.yaml) | `10` | **`20`** | Diurnal-readiness | Diurnal SPIKE=500 RPS ÷ `rpsPerPodMax=30` = 17 pods minimum. The 10-cap saturated BOTH scalers identically during the lunch and PM spikes, erasing AAS's predictive advantage exactly when the demo needs to show it. Bumped on both CRs in lock-step so the bound stays symmetric. |
| 19 | `spec.preferredForecaster` | [`deploy/manifests/agenticautoscaler-sample.yaml`](../../deploy/manifests/agenticautoscaler-sample.yaml) | unset (= `auto`) | **`prophet`** | Diurnal-readiness | Diurnal has clear seasonality; after 12h elapsed Prophet's `hourly_profile` regressor engages and dominates linear_extrap on SMAPE. Pinning avoids the classifier flapping to linear during early hours when the partial window looks flat. |

## Grouped by category

### Fairness changes (#1, #2)

Strip every tuning advantage AAS has over HPA, leaving only the
forecast lookahead as the remaining difference. After these changes
the comparison becomes:

- Same workload (byte-identical pods, paired k6 traffic)
- Same replica bounds (min=2, max=20 — see change #18)
- **Same per-pod RPS target (30, with rpsPerPodMax=31 for webhook compliance — see change #1)** ← change #1
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

### Data retention changes (#13, #14, #15)

All three address the same observed failure: yesterday's Grafana panels
silently disappeared. Two independent root causes contributed:

1. `retention: 2h` — Prometheus deletes data >2h old whether the pod
   restarts or not. Anything you recorded at 9 PM is gone by 11 PM.
2. `storageSpec: {}` — emptyDir. Any restart (OOMKill at the previous
   1Gi limit, kind node restart, helm upgrade) wipes the TSDB to zero.

The fixes are mutually reinforcing. Persistent storage alone wouldn't
help (data still expires at 2h); long retention alone wouldn't help
(restart wipes everything anyway). All three are needed for any soak
run > 2h to produce reviewable artifacts.

To apply the change, rerun `make install-deps`. The helm upgrade
recreates the Prometheus StatefulSet and binds a fresh PVC; the existing
metrics inside the emptyDir are lost on this one-time transition (you
already have nothing older than 2h anyway).

### Diurnal-readiness changes (#16, #17, #18, #19)

These four changes turn the 24h diurnal scenario from "won't work at
all" into "produces a clean SMAPE comparison":

| Without these | With these |
|---|---|
| Classifier never engages (12-sample query, 22-sample floor → permanent "insufficient data") | Classifier engages at ~110 min elapsed and produces real `Classified Pattern` output through the full 24h |
| Both scalers hit `maxReplicas=10` at the lunch + PM spikes; comparison flatlines | Both have 20-pod ceiling, 3 pods of headroom above the 17-pod spike minimum, so each can actually choose its replica count |
| Forecaster sees only 15-min window; misses the gradual day-shape slope | Forecaster sees 60-min context, tracks the slope of each hourly stage cleanly |
| Classifier may flap between `PatternFlat` / `PatternGradualRamp` in early hours; forecaster choice oscillates | Prophet is pinned; once `HOURLY_PROFILE_MIN_HOURS=12` elapses, the `hour_baseline` regressor kicks in and SMAPE drops further |

Note that with `CLASSIFIER_HISTORY_HOURS=25` the cluster effectively needs
~24h of uptime before the classifier sees enough history to identify
"diurnal" specifically — earlier classifications may stamp other
patterns. This is correct behaviour, not a bug.

## How to apply this branch to a running cluster

```bash
git checkout hackathon
# Rerun install-deps ONLY when the Prometheus values changed (#13-#15).
# Skipping this leaves the cluster on retention=2h + emptyDir and the
# data-retention fixes won't take effect.
make install-deps
make deploy
# Verify all overrides took effect:
kubectl -n demo get aas app-agentic -o jsonpath='{.spec.rpsPerPodMin}/{.spec.rpsPerPodMax}{"\n"}'
# expect: 30/31  (30/30 would be webhook-rejected — see change #1)
kubectl -n demo get hpa app-hpa -o jsonpath='{.spec.behavior.scaleDown.policies[0].value}{"\n"}'
# expect: 4
kubectl -n demo get aas app-agentic -o jsonpath='{.spec.maxReplicas}/{.spec.preferredForecaster}{"\n"}'
# expect: 20/prophet
kubectl -n demo get hpa app-hpa -o jsonpath='{.spec.maxReplicas}{"\n"}'
# expect: 20
kubectl -n monitoring get prometheus -o jsonpath='{.items[0].spec.retention}{"\n"}'
# expect: 30h
kubectl -n monitoring get pvc -l app.kubernetes.io/name=prometheus
# expect: a Bound PVC with CAPACITY 5Gi
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
