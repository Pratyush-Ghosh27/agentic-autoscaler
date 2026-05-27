# Migrating from v1 to v2

**Date:** 2026-05-26
**Applies to:** Operators upgrading from `docs/design.md` (v1) to `docs/design_v2.md` (v2).

This guide is read-once-during-upgrade; for steady-state reference, read `docs/design_v2.md` directly. Every change listed here is **additive at the API level** — existing v1 CRs continue to validate and reconcile under v2 without manual edits.

---

## CRD changes (additive)

| Change | Impact |
| --- | --- |
| `spec.preferredForecaster` enum gains `gbdt_quantile` | Existing CRs with `prophet`, `linear_extrap`, or `auto` are unaffected. New option is opt-in only — `auto` mode never selects it (see `design_v2.md` §5 "Auto-mode selection rules"). |
| `status.classifiedParams.context` sub-object added (`baselineRPS`, `peakP95RPS`, `trend24hSlope`, `hourlyProfile[24]`, `hourlyProfileValid`) | Read-only status field. Populated by ClassifierWorker on the next cold-path tick after upgrade; until then, hot path runs context-free (existing v1 behaviour). |
| `status.unboundedRecommended` field added | Read-only. Surfaces capacity-planning signal: when forecast asks for more replicas than `spec.maxReplicas` allows, this field shows what the forecast wanted. |
| Two new K8s-event reasoning tokens: `max_replicas_binding`, `min_replicas_binding` | Fire when CRD bounds are the binding constraint. Visible in `kubectl get events`. See `design_v2.md` §5 step 10. |

## Env-var changes

| Env var | v1 | v2 default | Notes |
| --- | --- | --- | --- |
| `CLASSIFIER_MIN_POINTS` | 70 | 72 | Raised so `MIN_POINTS >= L + 10` invariant holds at 5-min downsampling (`L = 12`). |
| `CLASSIFIER_HIGH_CONFIDENCE_POINTS` | (~4h) | 240 (~20h) | Re-anchored at 5-min cadence. |
| `PROPHET_MIN_POINTS` | 60 | 30 | Lowered so Prophet engages halfway through the warm-up rather than only at full window. |
| `FORECAST_HORIZON_MINUTES` | controller env (kept-in-sync) | Forecast Service env only | Controller reads it from each `/recommend` response's `horizon_minutes` field; no local copy needed. Drop from controller deployment. |
| `LINEAR_EXTRAP_RECENT_WEIGHT` | (new) | 0.7 | Replaces internal-only `LINEAR_EXTRAP_TREND_BLEND` references (v1 had no such env). Variable name pins polarity (`α` on the recent slope; trend gets `1 − α`). |
| `CONTEXT_DOWNSAMPLE_RESOLUTION_MIN` | (new; v1 cold path was 1-min) | 5 | Cold-path Prometheus query resolution. All `CLASSIFIER_*` point thresholds are sized for this default; rescale together. |
| `CV_GUARD_MEAN_RPS` | (new; v1 hardcoded 1.0) | 1.0 | Below this mean RPS, classifier `cv` feature is forced to 0. Operator-tunable per workload scale. |
| `RPS_PER_POD_NOISE_FLOOR_RPS` | (new; v1 hardcoded 10) | 10 | Per-pod ratio observation gate. Operators with intentionally-low-RPS workloads can lower this. |
| `GBDT_QUANTILE` | (new) | 0.90 | GBDT upper-quantile prediction target. p90 = "scale for a worse-than-typical burst." |
| `GBDT_MIN_POINTS` | (new) | 30 | Minimum history for `forecast_gbdt_quantile` (else falls back to linear). Mirrors `PROPHET_MIN_POINTS`. |
| `PROPHET_USE_HOURLY_REGRESSOR` | (new) | true | When true and `hourlyProfileValid == true`, Prophet adds `hour_baseline` as an external regressor. |
| `HOURLY_PROFILE_MIN_HOURS` | (new) | 12 | Distinct UTC hours of history required to mark `hourlyProfileValid: true`. |
| `CLASSIFIER_DEDUP_SECONDS` | (new) | 60 | Suppresses redundant rollout-triggered re-classifications. |

## Behaviour changes

| What changed | What to expect |
| --- | --- |
| Cold path runs at 5-min resolution (was 1-min) | Classifier uses fewer, smoother data points (288/24h instead of 1440/24h). `CLASSIFIER_MIN_POINTS=72` at 5-min ≈ 6h of history before the first classification commits. |
| Classifier writes `context` block (5 fields) | Hot path forwards context to Forecast Service on every `/recommend` call. Forecasters now anchor short-horizon predictions on long-horizon structure. |
| Prophet uses hourly regressor when context is valid | Predictions on periodic workloads now lock onto the daily cycle's level, not just trend + changepoints. |
| `forecast_linear_extrap` blends recent slope with `trend_24h_slope` | Reduces noise-driven extrapolation swings on short windows. Includes intercept-recompute around centroid (eliminates a slope-blend bias bug). |
| New forecaster: `gbdt_quantile` (opt-in) | For `spiky` workloads only. Either set `spec.preferredForecaster: "gbdt_quantile"` explicitly, or rely on classifier writing it for `pattern == "spiky"`. `auto` mode never selects it. |
| K8s Event `Reason` field uses PascalCase | `kubectl get events` shows `ScaleUp`, `StepCappedUp`, `MaxReplicasBinding`, etc. The snake_case form (`scale_up`, `step_capped_up`, `max_replicas_binding`) remains in the message body for log searchability. Update any Grafana / kubectl-greps that key on lowercase reasons. |
| New event reasons `MaxReplicasBinding` / `MinReplicasBinding` | Surface when CRD bounds are the binding constraint. With and without an actual replica change in the same reconcile (see `design_v2.md` §5 precedence rule 4 + §6.2 trigger rules). |
| Ring buffer seeds 5 copies of persisted `rpsPerPodCurrent` on restart (was 1) | Post-restart `rpsPerPod` median is more stable; fewer spurious scale decisions in the first ~6 reconciles after a controller restart. |
| Re-classification trigger watches `deployment.kubernetes.io/revision` annotation (was `metadata.generation`) | `/scale` patches no longer fire spurious re-classifications. Only real rollouts (image / env / command edits) trigger re-classification. |
| Webhook rejects `maxReplicas == minReplicas` (was: only `<`) | Pinning replicas via `min == max` is now rejected at admission. The `maxStepSize` formula's clamp range `[1, maxReplicas - minReplicas]` is no longer empty. |
| New annotation: `autoscaling.agentic.io/skip-context: "true"` | When set, reconciler omits `context` from `/recommend` (forces context-free behaviour). Useful for ad-hoc testing. Persists until cleared. |
| New Forecast Service metric: `forecast_dispatch_total{model_used}` | Cumulative count of successful `/recommend` dispatches, labelled by the **resolved** model (post-fallback). A `prophet → linear_extrap` fallback increments under `linear_extrap`, not `prophet`. Useful for Grafana panels and alerts that need to know which forecaster is actually serving traffic. The nightly E2E asserts on `model_used="gbdt_quantile" > 0` after a `preferredForecaster: gbdt_quantile` patch (Plan 18 — see `test/e2e/assertions-gbdt.sh`). |

## Upgrade procedure

1. **Before upgrading**
   - Note any custom `CLASSIFIER_MIN_POINTS` or `PROPHET_MIN_POINTS` overrides; v2 defaults differ from v1.
   - If your controller deployment sets `FORECAST_HORIZON_MINUTES`, plan to drop that env var (it moves to Forecast Service only).
   - If you grep on `kubectl get events` Reason fields with snake_case strings, plan to update those greps to PascalCase.

2. **During upgrade**
   - Apply the new CRD manifests (additive; no breaking schema change).
   - Deploy the new controller image and Forecast Service image.
   - Set the new Forecast Service env vars (`GBDT_QUANTILE`, `GBDT_MIN_POINTS`, `PROPHET_USE_HOURLY_REGRESSOR`, `LINEAR_EXTRAP_RECENT_WEIGHT`) on the Forecast Service deployment.
   - Set the new controller env vars (`CONTEXT_DOWNSAMPLE_RESOLUTION_MIN`, `CV_GUARD_MEAN_RPS`, `RPS_PER_POD_NOISE_FLOOR_RPS`, `HOURLY_PROFILE_MIN_HOURS`, `CLASSIFIER_DEDUP_SECONDS`) on the controller deployment, or accept defaults.

3. **After upgrade**
   - Within `CLASSIFIER_INTERVAL_MINUTES` (default 30 min), each existing CR's ClassifierWorker fires on its periodic timer and writes `status.classifiedParams.context`. Watch for `pattern_classified` events confirming the cycle ran.
   - Verify `kubectl get aas <name> -o json | jq .status.classifiedParams.context` returns all 5 fields.
   - If you want immediate context population after upgrade, set `autoscaling.agentic.io/reclassify: "true"` on each CR — this triggers an immediate classification cycle. The controller removes the annotation after a successful run.

4. **Update Grafana / kubectl greps for PascalCase**
   - Any custom Grafana panel or kubectl-event grep that filters on `Reason` (e.g. `kube_event_count{reason="scale_up"}`) needs to be updated to PascalCase (`reason="ScaleUp"`). The full mapping is implemented in [`internal/reasoning/tokens.go`](../internal/reasoning/tokens.go) (`PascalReason`) and pinned by `internal/reasoning/tokens_test.go::TestPascalReason_AllTokensHaveMapping`.
   - The snake_case form remains in the **message body** for log searchability — `kubectl get events -o yaml | grep scale_up` still works, but `--field-selector reason=scale_up` does not.

5. **Optional: scrape the new `forecast_dispatch_total` metric**
   - If you operate the Forecast Service, add `forecast_dispatch_total` (5 cardinality: `prophet`, `linear_extrap`, `gbdt_quantile`, plus the metric itself) to your Prometheus dashboards. The nightly E2E asserts on it (Plan 18); operators may want to alert on `rate(forecast_dispatch_total{model_used="prophet"}[10m]) == 0` over long windows as a forecast-service-down signal.

## Rollback

The v2 CRD schema is a strict superset of v1's. Rolling back to a v1 controller against v2-shaped CRs is safe — the v1 controller will simply ignore `status.classifiedParams.context` and `status.unboundedRecommended` (unknown fields are preserved by the apiserver and not pruned). No explicit migration step is required.
