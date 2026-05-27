# Design vs Shipped — Gap Report (v2)

**Date:** 2026-05-26
**Spec:** `docs/design_v2.md` at `a08bbf4f` (pass-6 reviewer fixes + revision-notes split)
**Code reviewed:** `cmd/`, `internal/`, `api/`, `forecast-service/`, `deploy/`, `config/` at `a08bbf4f`
**Reviewer:** read-through of `design_v2.md` (1102 lines) against the live implementation.

This report sits alongside `gap-report-v1.md`. v1 documented "what v1 design promised vs. what v1 shipped"; this report documents "what v2 spec promises vs. what's currently in the tree." It is the input document for the v2 brainstorm and the v2 plan files.

> **v1 inherited status (rolled forward from `gap-report-v1.md`).**
> All seven v1 gaps (G1 classifier wiring, G2 controller metrics, G3 target-app deployment label, G4 prometheus-adapter install, G5 Ollama-in-CI, G6 nightly tolerance 1.10, G7 k6 ramp inputs) are marked closed in v1's status footer. Spot-checked during this audit:
> - ✅ Classifier is wired into `main.go` (`cmd/controller/main.go:183-195`, `:206`).
> - ✅ Custom Prometheus metrics are registered (`internal/controller/metrics.go:51-145`).
> - ✅ prometheus-adapter is installed by `make install-deps` (`Makefile:282-284`).
> - ✅ Forecast Service exposes `/metrics` with `forecast_prophet_failures_total` (`forecast-service/src/forecast/app.py:43-46`).
> No new v1-inherited gaps surfaced during this audit.

> **Bottom line.** v1 closed its design gaps. v2 raises the bar: a third forecaster, a context object that flows from cold path → hot path → Forecast Service on every call, anchored Prophet timestamps, blended linear extrapolation with intercept recompute, raised classifier thresholds, new reasoning tokens for CRD-bound binding, a switched generation watcher, and a 5-copy ring-buffer seed. The largest single delta is the `context` plumbing — almost no v1 code path forwards classifier-derived context to the Forecast Service today, which is the foundation every other v2 forecasting improvement is built on.

---

## 1. What v1 ships, that v2 keeps unchanged

These v1 capabilities still hold in v2. The plan should not regress them.

| Capability | Evidence |
|---|---|
| `AgenticAutoscaler` CRD with the v1 §4 fields | `api/v1alpha1/agenticautoscaler_types.go` |
| Hot-path reconciler — Prometheus query → Forecast → `/scale` patch | `internal/controller/agenticautoscaler_controller.go` |
| `maxStepSize` cap and scale-up/down cooldowns + cooldown-overrides-cap precedence | `internal/decision/decision.go:184-222` |
| Hysteresis (no patch when `target == current`) | `internal/decision/decision.go:217-222` |
| Kill-switch annotation pre-check + `Disabled` phase | `internal/controller/agenticautoscaler_controller.go:115-118` |
| HPA conflict pre-check + `Conflict` phase | `internal/controller/agenticautoscaler_controller.go:125-130` |
| `forecast_unavailable` / `metrics_unavailable` no-op-and-retry | `internal/controller/agenticautoscaler_controller.go:135-156, 173-178` |
| ClassifierWorker manager goroutine per CR | `cmd/controller/main.go:183-195` + `internal/classifier/manager.go` |
| Custom Prometheus metrics (predicted_rps, scale_events_total, classified_pattern, ...) | `internal/controller/metrics.go:51-145` |
| ExplainWorker drop-and-replace channel + Ollama integration | `internal/explainer/worker.go` |
| Forecast Service Prophet + linear_extrap + auto + warm-up + Prometheus exporter | `forecast-service/src/forecast/dispatch.py`, `app.py` |
| Validating webhook with the v1 §4 rules + 17 webhook tests | `internal/webhook/v1alpha1/validator.go` |
| `target_app_*` metrics carry the `deployment` label | `target-app/internal/server/server.go` (per gap-report-v1 G3 closure) |

---

## 2. v2 gaps in priority order

Gap numbering continues from gap-report-v1 (which used G1–G7), so v2 gaps start at G10. This avoids any cross-doc collision when an issue is referenced by short ID (e.g., in a plan file).

### G10 (CRITICAL) — Forecast Service `context` is not plumbed end-to-end

**Spec.** `design_v2.md:206-212` (CR `status.classifiedParams.context` fields), `:489-499` (`/recommend` request body's `context` object), `:495-499` (per-request `current_hour_utc` and `current_minute_utc`). Prophet (`:629-642`), GBDT (`:670-690`), and `forecast_linear_extrap` (`:566-574` with the trend blend at step 3) all read from `context`.

**Code reality.**
- `api/v1alpha1/agenticautoscaler_types.go:101-129` `ClassifiedParams` has no `Context` field. The persisted classifier output is just `{Pattern, ScaleUpCooldownSeconds, ScaleDownCooldownSeconds, MaxStepSize, PreferredForecaster, ClassifiedAt, HistoryPoints, Confidence}`.
- `internal/classifier/worker.go:235-244` `patchStatus` writes those eight fields and nothing else. `baselineRPS`, `peakP95RPS`, `trend24hSlope`, `hourlyProfile`, `hourlyProfileValid` are never computed.
- `internal/adapters/forecast/types.go:16-23` `RecommendRequest` has no `Context` field; only `RpsHistory`, `WorkloadID`, `PreferredModel`.
- `internal/controller/agenticautoscaler_controller.go:168-172` builds the request without any context.
- `forecast-service/src/forecast/models.py:10-29` `RecommendRequest` accepts no `context`.
- `forecast-service/src/forecast/dispatch.py:27-67` `recommend(...)` ignores any context that might exist; just passes `rps_history`, `horizon_minutes`, `prophet_min_points`, `preferred_model`.

**Effect.** Every "long-horizon-anchored short-horizon prediction" claim in v2 §1's overview is impossible to satisfy until this is closed: Prophet has no hourly regressor (no daily structure to anchor on), `forecast_linear_extrap` has no trend prior to blend with, GBDT (when added — see G12) has no `hour_of_day_baseline` feature. v2's headline architectural improvement is inert.

**Severity.** Critical. This is the *root* of most other forecasting gaps in this report; G14 (Prophet anchoring), G15 (linear blend + intercept), G12 (GBDT hour features), and G11 (cold-path context computation) all depend on G10.

**Estimated fix.** 2 days. Schema change for `ClassifiedParams.Context` (with subfields), Forecast Service Pydantic model addition, controller forwarding logic, classifier-side computation. Each forecaster's actual *use* of the new fields is its own gap (G14, G15, G12) — G10 is just the wiring.

---

### G11 (CRITICAL) — Cold path runs at 1-min cadence, not v2's 5-min downsampling, and has none of v2's new features

**Spec.** `design_v2.md:263` `CONTEXT_DOWNSAMPLE_RESOLUTION_MIN=5`. `:756-757` PromQL subquery with `5m` step. `:976` `hourly_autocorr` lag formula `L = 60 / CONTEXT_DOWNSAMPLE_RESOLUTION_MIN` (default 12). `:976-979` autocorr gate `len < L+10` is independent of `CLASSIFIER_MIN_POINTS`; the latter must be `>= L+10`. `:782-790` `trend24hSlope` divided by `CONTEXT_DOWNSAMPLE_RESOLUTION_MIN` to land in **rps/min** (F18). `:261` `CLASSIFIER_MIN_POINTS=72`, `:262` `CLASSIFIER_HIGH_CONFIDENCE_POINTS=240`. `:983-988` `peak_to_trough = p99 / max(mean, 1.0)` (F28). `:992-1004` `gradual_ramp` rule uses **relative** threshold `abs(slope) * 1440 / max(mean, 1) > GRADUAL_RAMP_DAILY_DRIFT_FRAC` (default 0.20) (F26). `:996` `cv` zero-guard threshold named `CV_GUARD_MEAN_RPS=1.0` (F29).

**Code reality.**
- `internal/classifier/worker.go:191` calls Prometheus at `step: time.Minute` — 1-min cadence, 1440 points/24h, not 288.
- `internal/classifier/features.go:29` `TodLag = 60` (60 *samples*; only correct at 1-min cadence). At 5-min cadence the lag should be 12.
- `internal/classifier/features.go:73` `peakToTrough = p99 / (m + 1)` — old denominator (F28 unfixed).
- `internal/classifier/features.go:67-70` `cv = sd / m if m >= 1 else 0` — magic threshold 1.0 unnamed (F29 unfixed).
- `internal/classifier/features.go:171-188` `trendSlope` returns slope per sample — at 1-min cadence happens to equal rps/min, but at 5-min cadence would be 5× too large with no division (F18 unfixed).
- `internal/classifier/classify.go:27,41` `trendSlopeRampAbove = 2.0` *absolute* threshold — used as `abs(f.TrendSlope) > 2.0`. F26 replaced this with a relative threshold; absolute version fires only on 30×+ daily growth.
- `internal/classifier/params.go:19` `KTodDown = 0.5` — F13 spec name is `K_PERIODIC_DOWN`. Spec acknowledges this divergence; rename in code is tracked separately. The naming is inconsistent with the spec and with the `tod_correlation`-was-renamed-to-`hourly_autocorr` rename that already happened in `Features.TodCorrelation`.
- `internal/config/config.go:32,128` `ClassifierMinPoints` default 70. `:154-158` `validate()` enforces `< 70`. v2 says default 72 and the floor should derive from `L + 10` (= 22 at 5-min, = 70 at 1-min). The hardcoded 70 is a 1-min-cadence leftover.
- `internal/classifier/worker.go:235-244` `patchStatus` doesn't write context (covered by G10) — but specifically here, none of `baseline_rps`, `peak_p95_rps`, `trend_24h_slope`, `hourly_profile`, `hourly_profile_valid` is computed during the classification cycle. They'd all be net-new code in `features.go` / `pipeline.go`.

**Effect.** Even after G10 wires the schema and request through, the classifier has no values to *put* into the context. Plus everything that depends on cadence (autocorr lag, trend slope units, threshold semantics) silently breaks if cadence changes are made piecemeal. The current 1-min behaviour is internally consistent but doesn't deliver the v2 "long-horizon anchored" semantics.

**Severity.** Critical. Sibling to G10 — together they make v2's cold path real.

**Estimated fix.** 2–3 days. The cadence change ripples through `features.go` (constants), `pipeline.go` (autocorr gate), `classify.go` (`trendSlopeRampAbove` → relative threshold + new `mean`-derived computation), `params.go` (`KTodDown` → `KPeriodicDown`), `config.go` (defaults + new `CONTEXT_DOWNSAMPLE_RESOLUTION_MIN` env var + relaxed validate floor), `worker.go` (PromQL `step` and the new context fields). Tests need re-baselining at the new resolution.

---

### G12 (CRITICAL) — Third forecaster `gbdt_quantile` is missing entirely

**Spec.** `design_v2.md:7,11-12,40,42,76` overview / scope / arch lists three forecasters. `:281` `GBDT_MIN_POINTS=30` (default). `:543` *"`auto` mode never returns `gbdt_quantile`"* — explicit opt-in via classifier `pattern == "spiky"` or operator override. `:670-690` full pipeline pseudocode for `forecast_gbdt_quantile`: lag features, `hour_of_day_baseline`, `minute_in_hour`, training rows from history shifted by `FORECAST_HORIZON_MINUTES`, `LightGBMRegressor` at `GBDT_QUANTILE=0.90`, `peak_p95_rps × 3` safety cap.

**Code reality.**
- `forecast-service/src/forecast/dispatch.py:18` `ModelName = Literal["prophet", "linear_extrap"]` — only two.
- `forecast-service/src/forecast/dispatch.py:33-67` no GBDT branch in `recommend()`.
- No `gbdt_model.py` or equivalent in `forecast-service/src/forecast/`.
- `forecast-service/src/forecast/models.py:21,37` `preferred_model` and `model_used` literals omit `"gbdt_quantile"`.
- `api/v1alpha1/agenticautoscaler_types.go:77` CRD enum `+kubebuilder:validation:Enum=prophet;linear_extrap;auto` — no `gbdt_quantile`.
- `api/v1alpha1/agenticautoscaler_types.go:116` `ClassifiedParams.PreferredForecaster` enum `prophet;linear_extrap` — no `gbdt_quantile`.
- `internal/webhook/v1alpha1/validator.go:84` accepts only three values.
- `internal/classifier/params.go:30-34` `ForecasterLinearExtrap`, `ForecasterProphet` constants only; no `ForecasterGBDTQuantile`.
- `internal/classifier/params.go:83-87` forecaster selector is feature-driven (`tod > 0.70 OR |trend| > 2.0 → prophet`), never picks GBDT.

**Effect.** v2's "spiky workloads use a quantile predictor that absorbs short-burst tails" is unimplemented. The current `spiky` pattern (when `Classify` returns it) ends up using either `prophet` or `linear_extrap` from `params.go`'s feature-driven selector — neither is the right tool for the burst-prediction job v2 promises.

**Severity.** Critical. One of v2's two new forecaster paths (the other being F16's trend-blend in linear_extrap).

**Estimated fix.** 3 days. New Python file (`gbdt_model.py`); pipeline assembly (lag features, hour/minute features, train+predict); dispatch wiring (auto-never-picks rule, preferred_model bypass); CRD enum widening; Pydantic model widening; webhook update; classifier `params.go` pattern → forecaster mapping (G19).

---

### G13 (HIGH) — `recommendedReplicas` is clamped, no `max_replicas_binding` / `min_replicas_binding` reasoning tokens, no `unboundedRecommended` field

**Spec.** `design_v2.md:466-471` precedence rules: step 5 tentatively sets `max_replicas_binding`/`min_replicas_binding` when the unbounded recommendation falls outside `[minReplicas, maxReplicas]`; step 6 cap and step 7 cooldown may override; step 8 hysteresis suppresses the patch (and ExplainWorker trigger) but not the K8s event. `:479` step-10 token list includes the binding tokens. `:484` event message includes `unboundedRecommended` when it differs from `recommendedReplicas`. `:485` *`status.recommendedReplicas` is the pre-cap, pre-cooldown value computed in step 5*.

**Code reality.**
- `internal/decision/decision.go:137-149` `ComputeRecommended` clamps to `[minReplicas, maxReplicas]` internally — the unbounded value is discarded.
- `internal/controller/agenticautoscaler_controller.go:215` `recommended := decision.ComputeRecommended(...)` — only the clamped value is captured.
- `internal/controller/agenticautoscaler_controller.go:286` `aas.Status.RecommendedReplicas = recommended` — the spec calls this the unbounded recommendation, but here it's already clamped.
- `internal/decision/decision.go:184-222` `ApplyCapAndCooldown` has no branch that emits `MaxReplicasBinding`/`MinReplicasBinding`. Tokens don't exist (next bullet).
- `internal/reasoning/tokens.go:9-31` no `MaxReplicasBinding` / `MinReplicasBinding` constants. `AllTokens()` enumerates 14 tokens; v2 has 16.
- `api/v1alpha1/agenticautoscaler_types.go:144-181` `AgenticAutoscalerStatus` has no `UnboundedRecommended` field.

**Effect.** Operators have no signal that the CRD bound is the binding constraint. A workload that sustains "forecast wants 15 but `maxReplicas=10`" looks identical to a workload that's correctly running at 10 replicas. The capacity-planning intent of v2 F27 is not surfaced anywhere.

**Severity.** High. User-visible behaviour gap that flows through to events, status, and the ExplainWorker's prose (G18).

**Estimated fix.** 1 day. Split `ComputeRecommended` into unbounded + clamped variants; add the two tokens; thread `unboundedRecommended` through `CapOutput`, status, the event message, and the ExplainRequest plumbing (G18).

---

### G14 (HIGH) — Prophet uses local clock instead of request context for `ds`, and has no hourly regressor

**Spec.** `design_v2.md:498-499` `current_hour_utc` and `current_minute_utc` are required context fields. `:629-642` Prophet pipeline: ds is anchored so `(utc_hour, utc_minute)` of `ds[-1]` matches `(context.current_hour_utc, context.current_minute_utc)` (F3a + F17), service does not call its own clock. `:637-639` when `PROPHET_USE_HOURLY_REGRESSOR=true` and `hourly_profile_valid` is true, Prophet adds `hour_baseline` as an external regressor.

**Code reality.**
- `forecast-service/src/forecast/prophet_model.py:39` `end = datetime.now(tz=UTC).replace(...)` — local clock anchor. F3a/F17 unfixed.
- `forecast-service/src/forecast/prophet_model.py:42` `df = pd.DataFrame({"ds": timestamps, "y": rps_history})` — `y` only, no regressor column.
- `forecast-service/src/forecast/prophet_model.py:44-49` `Prophet(daily_seasonality=False, weekly_seasonality=False, ...)` — no `model.add_regressor("hour_baseline")` call.
- No `PROPHET_USE_HOURLY_REGRESSOR` env var anywhere.

**Effect.** Pod clock skew between controller and Forecast Service silently misaligns the hourly regressor's hour-of-day lookup — and there's no hourly regressor anyway, so currently Prophet has no daily-cycle structure to fit. Periodic workloads get the same ~10 random-walk-with-changepoints prediction that any non-periodic series would.

**Severity.** High. Prophet's main job in v2 (model the daily cycle) is unimplemented.

**Estimated fix.** 1 day. Anchor `ds` from request context (3 lines); add `add_regressor` + `make_future_dataframe` regressor population (5–10 lines + tests). Tied to G10 plumbing.

---

### G15 (HIGH) — `forecast_linear_extrap` has no trend-blend, no intercept recompute, fixed 10-point window

**Spec.** `design_v2.md:566-574` pipeline: window size = `min(LINEAR_EXTRAP_WINDOW_MINUTES, len(history))` (default 10). Step 2 fits `m, b` on the window. Step 3 *blends* `m` with `context.trend24hSlope`: `m_blended = (1 - LINEAR_EXTRAP_TREND_BLEND) * m + LINEAR_EXTRAP_TREND_BLEND * context.trend24hSlope` (F16) and **then recomputes** `b = mean(y) - m_blended * mean(x)` (F31 — without this the line rotates around `x=0` instead of the centroid, biasing predictions). Step 4 extrapolates and clips with `peak_p95_rps × 1.5`.

**Code reality.**
- `forecast-service/src/forecast/linear_extrap.py:27` `series = np.asarray(rps_history[-10:], dtype=float)` — fixed window, no env var.
- `forecast-service/src/forecast/linear_extrap.py:34` `slope, intercept = np.polyfit(x, series, deg=1)` — no blend.
- `forecast-service/src/forecast/linear_extrap.py:36-37` extrapolation with the unblended `slope, intercept`.
- `forecast-service/src/forecast/linear_extrap.py` no `LINEAR_EXTRAP_TREND_BLEND`, no `peak_p95_rps` clip, no centroid re-compute.

**Effect.** F16's noise reduction (long-horizon stabiliser for the otherwise-noisy 10-point slope) is not delivered; F31's bias fix has no effect because the blend it would correct doesn't exist; F18's unit pin (rps/min) goes unused at the consumer side. Linear extrapolation is the cheapest, most-frequently-selected forecaster in `auto` mode (anything with `len(history) < PROPHET_MIN_POINTS`); it's the workhorse path and it's the dumbest version of itself.

**Severity.** High. Easy fix, large impact on hot-path quality.

**Estimated fix.** Half a day. Six-line code change inside `forecast_linear_extrap`, plus an env var and a unit test that asserts the centroid-anchored property.

---

### G16 (HIGH) — Re-classification trigger watches `Deployment.metadata.generation`, which bumps on every `/scale` patch

**Spec.** `design_v2.md` revision-notes Pass 3 F19 (and §6.1's three-trigger list): the third re-classification trigger is the `deployment.kubernetes.io/revision` annotation, which only changes on actual rollouts (image / env / command edits) — not on `/scale` patches.

**Code reality.**
- `internal/controller/agenticautoscaler_controller.go:189-191` `r.Classifier.ObserveDeploymentGeneration(req.NamespacedName, deploy.Generation)` — passes `deploy.Generation`.
- `Deployment.metadata.generation` is bumped by the API server on every spec mutation, including the `/scale` subresource patches the controller itself issues every reconcile when a scale happens.

**Effect.** As written, every reconcile that scales fires a re-classification signal. The `CLASSIFIER_DEDUP_SECONDS` window (default 60s) hides most of the damage — but during sustained ramp / busy reconcile cycles, this still defeats the trigger's purpose (catch genuine rollouts) and trades classifier CPU for nothing useful.

**Severity.** High. Logic bug in a v1-shipped feature that only became visible during the v2 audit.

**Estimated fix.** Half a day. Switch from `deploy.Generation` to a hash/string of `deploy.Annotations["deployment.kubernetes.io/revision"]`; update the `Manager.ObserveDeploymentGeneration` signature accordingly (and its dedup map's key type). Add an envtest that proves a `/scale` patch does NOT signal re-classification.

---

### G17 (MEDIUM) — Ring buffer seeded with 1 copy of persisted `rpsPerPodCurrent` instead of 5

**Spec.** `design_v2.md` revision-notes Pass 3 F20: seed the per-CR `rps_per_pod` ring buffer with **5 copies** of the persisted value on restart so the median is preserved across the next 5+ observations, not washed out by the second new sample.

**Code reality.**
- `internal/decision/state.go:47-49` `Seed(v float64) { rb.data = append(rb.data[:0], v) }` — single copy.
- `internal/decision/decision.go:272` `state.Observations.Seed(seed.RpsPerPodCurrent)` — invoked once per restart.
- `internal/controller/agenticautoscaler_controller.go:43` `ringBufferCapacity = 10` — buffer holds 10 entries; seeding 1 means the persisted value loses majority within ~6 new observations.

**Effect.** Right after a controller restart, the persisted `rpsPerPodCurrent` is overwhelmed by fresh-and-possibly-noisy observations. For a 60s reconcile interval, the persisted value's influence vanishes within ~6 minutes — long enough for the post-restart cooldown to expire and a misguided scale decision to fire on a wobbly median. The longer-term goal of "restart recovery preserves the operator's runtime calibration" is silently undermined.

**Severity.** Medium. Bug only surfaces on controller restart during steady-state operation.

**Estimated fix.** ~1 hour. Change `Seed(v)` to push v five times (or take a count parameter); update its single test.

---

### G18 (MEDIUM) — ExplainWorker prompt missing `Long-term context` line and binding-token conditional; `ExplainRequest` missing fields

**Spec.** `design_v2.md` §6.2 prompt template: include a `Long-term context: baseline=B, peak=P, hour-of-day baseline=H, slope=S` line whenever `Pattern != ""` (F12 — explicitly *not* gated on `BaselineRPS == 0 && PeakP95RPS == 0`). Pass 5 F33 added prompt conditionals for `max_replicas_binding`/`min_replicas_binding` reasoning tokens that surface `unboundedRecommended` and the binding CRD bound — and added `UnboundedRecommended`, `MaxReplicas`, `MinReplicas` to `ExplainRequest`.

**Code reality.**
- `internal/explainer/prompt.go:51-54` triggers off `req.Pattern != "" && req.Pattern != "default"` — diverges from F12's `Pattern != ""` (current code treats `"default"` as un-classified, which is a bug under F12). The prompt also lacks any `Long-term context` line — even when the gate fires, the prose includes only `Traffic pattern: <name> (confidence: <level>)` (one line), then the same hot-path facts every reconcile gets.
- `internal/explainer/prompt.go:61-65` only the `step_capped_*` branch exists. No `max_replicas_binding`/`min_replicas_binding` prompt-conditional.
- `internal/controller/interfaces.go:39-55` `ExplainRequest` has no `UnboundedRecommended`, `MaxReplicas`, `MinReplicas`, `BaselineRPS`, `PeakP95RPS`, `HourlyProfile`, `Trend24hSlope` fields.

**Effect.** The LLM-generated prose has no long-horizon context to ground its explanation in (every prompt looks like the workload was just born); when scale events fire on the CRD bound, the prose generates misleading "scaled up to handle load" text rather than the F33-fixed "this is at the cap; raise `spec.maxReplicas` if you want more headroom" framing.

**Severity.** Medium. Doesn't affect scaling decisions. Affects only operator-visible explanations, but that's the entire point of the ExplainWorker.

**Estimated fix.** Half a day. Extend `ExplainRequest`, populate from reconciler step 10, update prompt template with the new conditionals + context line, update `prompt_test.go`. Depends on G10 (context plumbing) and G13 (binding tokens + unboundedRecommended).

---

### G19 (MEDIUM) — Classifier forecaster selector is feature-driven, not pattern-driven

**Spec.** `design_v2.md:1023-1056` (the classification + forecaster mapping table): `flat → linear_extrap`, `periodic → prophet`, `spiky → gbdt_quantile`, `gradual_ramp → linear_extrap` (with the trend prior from F16 doing the work), `default → linear_extrap`.

**Code reality.**
- `internal/classifier/params.go:83-87` selector is `if f.TodCorrelation > 0.70 OR |f.TrendSlope| > 2.0 → prophet else linear_extrap`. Never picks `gbdt_quantile`. Picks `prophet` for `gradual_ramp` (when |slope| > 2.0) — the wrong forecaster per the v2 mapping. Picks `linear_extrap` for some `spiky` workloads (when `tod ≤ 0.70` and `|slope| ≤ 2.0` but `cv > 0.50` and `peak_to_trough > 5`) — also wrong.
- `internal/classifier/classify.go:33-46` already produces the named pattern. `pipeline.go:39` already calls `Classify(f)`. The pattern is *available*; it just isn't used by `ComputeParams` to pick the forecaster.

**Effect.** Each pattern's intended forecaster doesn't fire reliably. v2's "spiky → gbdt_quantile" can't fire at all (G12) but even after G12 lands, the selector here would still need to be rewritten.

**Severity.** Medium. Functional gap, not a behaviour-bug — current selection happens to coincide with the right answer for `flat` and `periodic` workloads but fails for `spiky` and `gradual_ramp`.

**Estimated fix.** ~2 hours after G12 lands. `ComputeParams` takes `pattern string` as input (or `RunPipeline` becomes the orchestrator) and returns the pattern → forecaster table directly. New unit tests covering each pattern's chosen forecaster.

---

### G20 (MEDIUM) — Webhook accepts `maxReplicas == minReplicas`; CRD enums missing `gbdt_quantile`

**Spec.** `design_v2.md:217` admission rule rejects `maxReplicas <= minReplicas` (F37 — strict inequality). `:166,180` `preferredForecaster` enum is `prophet | linear_extrap | gbdt_quantile | auto`.

**Code reality.**
- `internal/webhook/v1alpha1/validator.go:36-41` only rejects `maxReplicas < minReplicas`; equal is allowed.
- `internal/webhook/v1alpha1/validator.go:84` enum check accepts only `prophet, linear_extrap, auto`.
- `api/v1alpha1/agenticautoscaler_types.go:77,116` kubebuilder enum markers list three values, not four.

**Effect.** Operator can set `min == max` and the classifier's `maxStep = clamp(_, 1, 0)` runs into the degenerate clamp v2 F37 was designed to prevent. Operator cannot opt into `gbdt_quantile` even after G12 lands.

**Severity.** Medium. Trivial schema/validator change; both must land with G12 to make the third forecaster operator-selectable.

**Estimated fix.** 1 hour. Tighten the inequality, widen both enums (kubebuilder marker + Go switch), regenerate manifests, update `validator_test.go`'s 17 cases.

---

### G21 (MEDIUM) — Env-var defaults out of sync with v2; several v2 env vars don't exist

**Spec.** `design_v2.md:241-279` controller and Forecast Service env-var tables. Notable defaults vs. controller values today:

| Env var | v1/code default | v2 default | Owner |
|---|---|---|---|
| `CLASSIFIER_MIN_POINTS` | 70 (`config.go:128`) | 72 | controller |
| `PROPHET_MIN_POINTS` | 60 (`config.go:124`, `app.py:20`) | 30 | service |
| `RPS_PER_POD_NOISE_FLOOR_RPS` | hardcoded 10 (`decision.go:231`) | env, default 10 | controller |
| `CV_GUARD_MEAN_RPS` | hardcoded 1.0 (`features.go:68`) | env, default 1.0 | controller |
| `CONTEXT_DOWNSAMPLE_RESOLUTION_MIN` | implicit 1 (cold path uses 1-min) | env, default 5 | controller |
| `GBDT_QUANTILE` | not present | 0.90 | service |
| `GBDT_MIN_POINTS` | not present | 30 | service |
| `PROPHET_USE_HOURLY_REGRESSOR` | not present | true | service |
| `LINEAR_EXTRAP_TREND_BLEND` | not present | 0.30 | service |
| `LINEAR_EXTRAP_WINDOW_MINUTES` | hardcoded 10 (`linear_extrap.py:27`) | env, default 10 | service |
| `FORECAST_HORIZON_MINUTES` | controller env (`config.go:122`) AND service env (`app.py:19`) | service-only (F36) | service |

**Code reality.** See the citations above. `config.go:154-158` *also* validates `CLASSIFIER_MIN_POINTS >= 70` with a comment about the 60-point lag — that floor is the 1-min-cadence remnant from G11 and needs to soften to "≥ `60 / CONTEXT_DOWNSAMPLE_RESOLUTION_MIN + 10`" or just drop in favour of the named `L + 10` invariant.

**Effect.** Operators can't tune any of the new dials. Out-of-sync defaults give classifications that don't match the spec (the validate-floor of 70 *prevents* the v2-recommended default of 72 from ever taking effect, since 72 > 70 satisfies the gate but the same gate would reject 22 if the operator tried to scale `MIN_POINTS` down to match a tuned `CONTEXT_DOWNSAMPLE_RESOLUTION_MIN`).

**Severity.** Medium. Mostly mechanical, but several lines need to land together to keep the system internally consistent.

**Estimated fix.** Half a day spread across `config.go` (controller-side adds/removes/defaults/validate), `app.py` (service-side adds), Helm/Kustomize manifests, the `Summary()` formatter, and the unit tests covering env parsing.

---

### G22 (LOW) — K8s Event `Reason` field uses snake_case directly

**Spec.** `design_v2.md:484` step-10 bullet on K8s Event `Reason` naming: snake_case tokens are the canonical identifiers and go into the message body; the `Reason` field uses PascalCase per K8s convention (`ScaleUp`, `StepCappedUp`, `MaxReplicasBinding`, …).

**Code reality.**
- `internal/reasoning/tokens.go:9-31` constants name PascalCase but value snake_case.
- `internal/controller/agenticautoscaler_controller.go:250` `r.EventRecorder.Eventf(&aas, corev1.EventTypeNormal, capOut.Reason, ...)` — `capOut.Reason` is the snake_case string — passed straight in as the K8s `Reason` field.
- Same pattern at `:137,147,153,175,311,346` and in `internal/classifier/worker.go:208,221`.

**Effect.** Cosmetic-but-public. K8s Event Reason is a stable wire field surfaced by `kubectl describe` and `kubectl get events`. Tools and runbooks that grep on PascalCase reasons (the K8s ecosystem default) won't match. Internal Grafana panels and unit tests would all need to update too.

**Severity.** Low. No functional regression; just a doc-vs-code naming convention divergence.

**Estimated fix.** ~3 hours. Add a `PascalCase()` accessor to each token (or a parallel constant block); replace `Reason: <token>` call sites; update the test suite that snapshots `AllTokens()`. Optional: also keep the snake_case form in the message body so log searches stay easy.

---

### G23 (DOC-ONLY) — Findings already marked as no-code-change

The following v2 findings are documentation-level only and require no implementation work, but are listed here so the v2 plan files can mark them out-of-scope rather than rediscovering them as gaps:

- **F8** (illusory test path deleted from §8) — doc only.
- **F10** (`current_hour_utc` is per-request) — doc only; the field already isn't persisted in `ClassifiedParams.Context` so the implementation matches the doc by absence.
- **F11** (`historyPoints: 1440 → 264` example fix) — doc only.
- **F25** (cross-reference between §7 `trend_slope` and §6.1 `trend24hSlope`) — doc only.
- **F30** (priority-rationale paragraph for `periodic > spiky`) — doc only.
- **F34** (precedence rule 4 wording) — doc only; the underlying behaviour (events fire even when `target == current_replicas` after step 7) was already correct in code.
- **F35** (status example arithmetic) — doc only.
- **F38** ("if the cap was hit" → "if the cap clipped target") — doc only; matches the existing `step_capped_*` semantics in `decision.go`.
- **F39** is **G22** above (this is the one F-finding from "doc-only" framing that does have a code component, listed separately).
- **F40** (§1 enumeration drift) — doc only.

---

## 3. v1 inherited gaps still open in v2 scope

None. Per the status footer of `gap-report-v1.md`, all seven v1 gaps closed in `v1.1.0` / `fix/v1.2-followup` / `fix/g6-tighten-nightly-tolerance`, and a sampling of those closures was re-verified in this audit (see the v1 inherited status block at the top). v2 starts from a clean v1 baseline.

The classifier subsystem (G1 in v1) is wired and functioning, but most of v2's classifier-side work in this report (G11, G19) builds *on top of* that wiring rather than against it.

---

## 4. Recommended v2 sequence

The dependency order is mostly forced. Treat the buckets as one PR each (or one plan-doc each), gated by the existing PR CI + nightly E2E. The first three are foundational and should land in the listed order; the rest are independent within their bucket but each depends on the bucket above.

1. **Foundations** — must land first; everything else depends on at least one of these.
   - **G10** — wire `Context` end-to-end through the CRD, the controller adapter, and the Forecast Service Pydantic model. No behaviour change yet; just the type plumbing.
   - **G11** — implement the cold-path computations that *fill* the new context fields (cadence change, new features, raised thresholds, renamed constants). Tests rebaseline at the new resolution.
   - **G21** — env-var pruning / additions / defaults realignment, including the relaxed `CLASSIFIER_MIN_POINTS` floor. Lands together with G11 because the new env vars are what tune the new behaviours.

2. **Forecaster surface** — once G10/G11/G21 are in place.
   - **G12** — third forecaster (`gbdt_quantile`). New Python file; new constant; CRD enum widen; webhook widen.
   - **G14** — Prophet anchoring + hourly regressor.
   - **G15** — linear extrapolation trend blend + intercept recompute + window env var.
   - **G19** — switch the classifier-side forecaster picker to pattern → forecaster (depends on G12 for the table to be complete).

3. **Operator-visibility** — these are independent of forecaster details.
   - **G13** — surface `unboundedRecommended` + `MaxReplicasBinding` / `MinReplicasBinding` tokens (depends on G10 for the status field's eventual home in the schema, but G13's slice of the schema is small and could fork off earlier).
   - **G18** — ExplainWorker prompt updates and `ExplainRequest` field additions (depends on G10 for context fields and G13 for binding fields).

4. **Bug-fix sweep** — tiny, can ship independently any time.
   - **G16** — switch generation watcher from `deploy.Generation` to revision annotation.
   - **G17** — ring-buffer 5-copy seed.
   - **G20** — webhook strict inequality + `gbdt_quantile` enum widening (couples to G12).
   - **G22** — K8s Event `Reason` PascalCase migration.

**Total estimate.** ~10–12 days of focused engineering for a single contributor, distributed across roughly five PRs (one per bucket, with G16/G17/G20/G22 grouped into one bug-fix-sweep PR). Each PR should be gated by the existing nightly E2E; once G12 is in, the nightly should be expanded with a `spiky` scenario that asserts `model_used == "gbdt_quantile"` to lock in the third-forecaster guarantee.

If the v2 release scope shrinks: **G10 + G11 + G21 + G15 + G16 + G17** are the smallest set that delivers a meaningfully more accurate v2 hot path without taking on the GBDT/`Context`-writes-from-classifier work in earnest. That subset is ~5 days and ships as one PR sequence.

---

## 5. Closure status (2026-05-27)

All v2 gaps G10–G22 are closed. G23 was doc-only and required no code work. The closing plan for each gap:

| Gap | Severity | Closed by |
| --- | --- | --- |
| G10 — Forecast Service `context` end-to-end plumbing | CRITICAL | Plan 13 (Phase 2 — v2 Foundations) |
| G11 — Cold-path 5-min cadence + new features + raised thresholds | CRITICAL | Plan 13 (Phase 2 — v2 Foundations) |
| G12 — Third forecaster `gbdt_quantile` | CRITICAL | Plan 14 (Phase 3 — v2 Forecaster Surface) |
| G13 — `recommendedReplicas` clamp + binding tokens + `unboundedRecommended` | HIGH | Plan 15 (Phase 4 — v2 Operator Visibility) |
| G14 — Prophet `ds` anchoring + hourly regressor | HIGH | Plan 14 (Phase 3 — v2 Forecaster Surface) |
| G15 — Linear extrap blend + intercept recompute + window env | HIGH | Plan 14 (Phase 3 — v2 Forecaster Surface) |
| G16 — Generation watcher → revision annotation | HIGH | Plan 16 (Phase 5 — v2 Bug-fix Sweep) |
| G17 — Ring buffer 5-copy seed | MEDIUM | Plan 16 (Phase 5 — v2 Bug-fix Sweep) |
| G18 — ExplainWorker prompt context + binding-token conditionals | MEDIUM | Plan 15 (Phase 4 — v2 Operator Visibility) |
| G19 — Pattern-driven forecaster selector | MEDIUM | Plan 14 (Phase 3 — v2 Forecaster Surface) |
| G20 — Webhook strict inequality + CRD enum widen | MEDIUM | Plan 14 (Phase 3 — enum widen) + Plan 16 (Phase 5 — strict inequality) |
| G21 — Env-var defaults realignment | MEDIUM | Plan 13 (Phase 2 — v2 Foundations) |
| G22 — K8s Event Reason PascalCase | LOW | Plan 16 (Phase 5 — v2 Bug-fix Sweep) |
| G23 — Doc-only findings | DOC | Plan 12 (Phase 1 — v2 Spec Edits) |

Per-criterion test coverage is enumerated in `docs/v2-acceptance-coverage.md`.

This footer is the formal close-out of `gap-report-v2.md`. Future v2.x audits should produce a new gap report (e.g., `gap-report-v2.1.md`) rather than reopening this one.
