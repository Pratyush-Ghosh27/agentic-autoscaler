# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For an operator-facing upgrade guide, see
[`docs/migrating-v1-to-v2.md`](docs/migrating-v1-to-v2.md). For the full v2
acceptance-criteria traceability and spec-vs-shipped audit, see
[`docs/v2-acceptance-coverage.md`](docs/v2-acceptance-coverage.md) and
[`docs/gap-report-v2.md`](docs/gap-report-v2.md) respectively.

---

## [v2.0.0] — 2026-05-28

First v2-spec-complete release. Implements
[`docs/design_v2.md`](docs/design_v2.md) (Pass 6) in full: third forecaster,
end-to-end classifier `context` plumbing, anchored Prophet predictions,
capacity-planning signals, and a bug-fix sweep against v1.

**Every change is additive at the CRD level.** Existing v1 CRs validate and
reconcile under v2 without manual edits. The v2 CRD schema is a strict
superset of v1's, so rolling back to a v1 controller against v2-shaped CRs is
safe (the v1 controller ignores unknown `status.*` fields).

### Added

- **Third forecaster: `gbdt_quantile`**, a LightGBM upper-quantile predictor
  (p90 by default) for spiky workloads where Prophet's seasonality fit lags
  burst behaviour. Auto-selected when the cold-path classifier identifies a
  `spiky` pattern via the G19 pattern → forecaster table in
  [`internal/classifier/params.go`](internal/classifier/params.go) (~T+6h
  on a fresh CR), or explicitly pinned via
  `spec.preferredForecaster: gbdt_quantile` to skip the classifier wait.
  F22 constrains only the Forecast Service's length-based auto branch
  (rule 3 in [`forecast-service/src/forecast/dispatch.py`](forecast-service/src/forecast/dispatch.py)),
  which never reaches gbdt_quantile when `preferred_model` is empty —
  the controller-side auto path *is* able to. ([G12], [F22], [G19])
- **`status.classifiedParams.context` block** that carries long-horizon
  structure from the ClassifierWorker to the hot path on every `/recommend`
  call: `baselineRPS`, `peakP95RPS`, `trend24hSlope`, `hourlyProfile[24]`,
  `hourlyProfileValid`. ([G10], [G11])
- **`status.unboundedRecommended`** field plus matching `MaxReplicasBinding`
  / `MinReplicasBinding` event reasons. Fire when the CRD bounds are the
  binding constraint, surfacing a capacity-planning signal that v1 dropped
  silently. ([G13], [F27])
- **Prophet hourly regressor** that engages when `hourlyProfileValid` is true
  and `PROPHET_USE_HOURLY_REGRESSOR=true` (default). Anchors short-horizon
  predictions on the workload's daily cycle, with `ds[-1]` anchored to the
  controller-provided `(current_hour_utc, current_minute_utc)` rather than
  the Forecast Service's local clock. ([G14], [F3a], [F17])
- **`forecast_linear_extrap` recent-slope / long-trend blend** plus
  intercept-recompute around the window centroid (fixes a rotate-at-x=0 bias
  that pulled extrapolations toward zero on positive-sloped histories).
  Tuned by new `LINEAR_EXTRAP_RECENT_WEIGHT` env (default 0.7). ([G15],
  [F16], [F31])
- **`forecast_dispatch_total{model_used}` Prometheus counter** on the
  Forecast Service. Labels by the *resolved* model (post-fallback) — a
  `prophet → linear_extrap` fallback increments `linear_extrap`, not
  `prophet`. Asserted by the nightly E2E (see Operational).
- **`autoscaling.agentic.io/skip-context` annotation** that forces
  context-free `/recommend` calls. Useful for A/B-testing forecaster
  behaviour with and without context.
- **PascalCase `PascalReason()` accessor** in
  [`internal/reasoning/tokens.go`](internal/reasoning/tokens.go) (see
  Changed for the breaking impact).

### Changed

- **K8s Event `Reason` field is now PascalCase** (`ScaleUp`, `StepCappedUp`,
  `MaxReplicasBinding`, …). The snake_case form (`scale_up`, …) remains in
  the message body for log searchability, but
  `kubectl get events --field-selector reason=…` filters and Grafana panels
  keyed on `kube_event_count{reason=…}` **must be updated to PascalCase**.
  ([G22], [F39])
- **Cold path runs at 5-minute resolution** (was 1-minute). 288 samples/24h
  instead of 1440 — smoother features, fewer Prometheus queries. The
  `CLASSIFIER_MIN_POINTS=72` default at 5-min ≈ 6h of history before the
  first classification commits. ([G11])
- **Re-classification trigger watches the
  `deployment.kubernetes.io/revision` annotation** (was
  `metadata.generation`). `/scale` patches no longer fire spurious
  re-classifications; only real rollouts (image / env / command edits)
  trigger one. ([G16], [F19])
- **Ring buffer seeds 5 copies of `rpsPerPodCurrent` on controller restart**
  (was 1). Post-restart median is stable across the next ~6 reconciles
  rather than washed out by the second new observation. ([G17], [F20])
- **Forecaster selector is pattern-driven** in
  [`internal/classifier/params.go`](internal/classifier/params.go):
  `flat | gradual_ramp | default → linear_extrap`,
  `periodic → prophet`, `spiky → gbdt_quantile`. Replaces v1's
  feature-driven selector that occasionally picked `prophet` for
  `gradual_ramp` and never picked `gbdt_quantile`. ([G19])
- **Webhook strict inequality:** `maxReplicas == minReplicas` is rejected at
  admission (was: only `<` rejected). v1 CRs already obeying this remain
  unaffected. ([G20], [F37])
- **CRD `spec.preferredForecaster` enum widened** to accept `gbdt_quantile`
  (was: `prophet | linear_extrap | auto`). ([G12], [G20])
- **Default env vars realigned** to match `design_v2.md` §4. Notable:
  `CLASSIFIER_MIN_POINTS` (70 → 72), `PROPHET_MIN_POINTS` (60 → 30). Several
  v1-hardcoded knobs (`CV_GUARD_MEAN_RPS`,
  `RPS_PER_POD_NOISE_FLOOR_RPS`, cold-path resolution) are now
  operator-tunable. Full table in
  [`docs/migrating-v1-to-v2.md`](docs/migrating-v1-to-v2.md#env-var-changes).
  ([G21])
- **Classifier `peak_to_trough` denominator is `max(mean, 1.0)`** (was
  `mean + 1`). Yields stable values across very-low-RPS workloads. ([F28])
- **`gradual_ramp` rule uses a relative drift threshold**:
  `abs(slope) * 1440 / max(mean, 1) > GRADUAL_RAMP_DAILY_DRIFT_FRAC`
  (default 0.20). v1's absolute threshold (`|slope| > 2.0`) only fired on
  30×+ daily growth. ([F26])

### Removed

- **`FORECAST_HORIZON_MINUTES` env var on the controller deployment.** It
  now lives on the Forecast Service deployment only; the controller reads
  it from each `/recommend` response's `horizon_minutes` field. **Drop it
  from controller helm values / kustomize overlays during the upgrade.**
  ([F36])

### Fixed

- **`linear_extrap` slope-blend intercept bias** — see Added /
  `forecast_linear_extrap`. ([G15], [F31])
- **Spurious classifier wake-ups on every `/scale` patch** — see Changed /
  revision-annotation watcher. ([G16])
- **Post-restart `rpsPerPod` median washed out within ~6 reconciles** — see
  Changed / 5-copy seed. ([G17])
- **`Features.TodCorrelation` Go field name** mismatched the
  `tod_correlation → hourly_autocorr` spec rename. Renamed to
  `Features.HourlyAutocorr` alongside helper `hourlyAutocorr` and the
  threshold constant `hourlyAutocorrAbove`. Internal Go-API only; no
  operator impact. ([F13] follow-up)

### Operational

- **Nightly E2E asserts the `gbdt_quantile` path end-to-end** by patching
  `aas/app-agentic` with `preferredForecaster: gbdt_quantile`, running the
  spiky k6 scenario, then asserting
  `forecast_dispatch_total{model_used="gbdt_quantile"} > 0`. (Plan 18;
  [`test/e2e/assertions-gbdt.sh`](test/e2e/assertions-gbdt.sh))
- **Nightly steady-state sleep raised to 12 min** so the AAS hot path
  completes its `HOT_PATH_MIN_POINTS=10` warm-up before k6 starts ramping.
  Eliminates a structural 5xx contributor that previously tripped the SLO
  threshold during ramp-up.
- **Per-side `http_req_failed` SLO thresholds** in `k6/scenarios/ramp.js`
  and `k6/scenarios/spiky.js` (`{url:agentic}` strict, `{url:hpa}`
  permissive). The previous global threshold conflated AAS performance with
  a structural prometheus-adapter weakness on the HPA side
  (`status=~"2.."` filter excludes 503 capacity signals; `[1m]` rate window
  vs 30s scrape interval). Documented inline in both scenarios.
- **Nightly failure-artifact bundle now includes**
  `hpa.yaml` + `describe-hpa-resource.txt` for first-class HPA-side
  diagnosis.
- **`deploy/k6/run-incluster.sh`** now polls Job status with `succeeded` /
  `failed` checks rather than `kubectl wait --for=condition=complete`,
  ensuring failed k6 scenarios exit quickly and leave artifact collection
  reachable.

### Documentation

- New: [`docs/design_v2.md`](docs/design_v2.md) (canonical v2 spec, Pass 6;
  1102 lines).
- New: [`docs/migrating-v1-to-v2.md`](docs/migrating-v1-to-v2.md)
  (operator upgrade guide).
- New: [`docs/v2-acceptance-coverage.md`](docs/v2-acceptance-coverage.md)
  (26-criterion test traceability matrix).
- New: [`docs/gap-report-v2.md`](docs/gap-report-v2.md) (spec-vs-shipped
  audit; all 13 v2 gaps closed at footer).
- New: [`docs/v2-spec-revision-plan.md`](docs/v2-spec-revision-plan.md) and
  [`docs/v2_revision notes.md`](docs/v2_revision%20notes.md) — F-finding
  history.
- Preserved: [`docs/design.md`](docs/design.md) as the v1 reference spec.

[v2.0.0]: https://github.com/Pratyush-Ghosh27/agentic-autoscaler/releases/tag/v2.0.0
[G10]: docs/gap-report-v2.md
[G11]: docs/gap-report-v2.md
[G12]: docs/gap-report-v2.md
[G13]: docs/gap-report-v2.md
[G14]: docs/gap-report-v2.md
[G15]: docs/gap-report-v2.md
[G16]: docs/gap-report-v2.md
[G17]: docs/gap-report-v2.md
[G19]: docs/gap-report-v2.md
[G20]: docs/gap-report-v2.md
[G21]: docs/gap-report-v2.md
[G22]: docs/gap-report-v2.md
[F3a]: docs/v2-spec-revision-plan.md
[F13]: docs/v2-spec-revision-plan.md
[F16]: docs/v2-spec-revision-plan.md
[F17]: docs/v2-spec-revision-plan.md
[F19]: docs/v2-spec-revision-plan.md
[F20]: docs/v2-spec-revision-plan.md
[F22]: docs/v2-spec-revision-plan.md
[F26]: docs/v2-spec-revision-plan.md
[F27]: docs/v2-spec-revision-plan.md
[F28]: docs/v2-spec-revision-plan.md
[F31]: docs/v2-spec-revision-plan.md
[F36]: docs/v2-spec-revision-plan.md
[F37]: docs/v2-spec-revision-plan.md
[F39]: docs/v2-spec-revision-plan.md

---

## [v1.1.0]

v1 stabilisation pass that closed the gaps surfaced by
[`docs/gap-report-v1.md`](docs/gap-report-v1.md):

- Classifier wired into `cmd/controller/main.go`.
- Controller-side custom Prometheus metrics registered
  ([`internal/controller/metrics.go`](internal/controller/metrics.go)).
- `target_app_*` metrics carry the `deployment` label.
- `prometheus-adapter` installed via `make install-deps`.
- Forecast Service `/metrics` exposes `forecast_prophet_failures_total`.
- Nightly E2E tolerance tightened to release-gate strictness.
- k6 ramp-scenario inputs cleaned up (`workflow_dispatch` ergonomics).

[v1.1.0]: https://github.com/Pratyush-Ghosh27/agentic-autoscaler/releases/tag/v1.1.0

## [v1.0.0]

Initial release per [`docs/design.md`](docs/design.md).

[v1.0.0]: https://github.com/Pratyush-Ghosh27/agentic-autoscaler/releases/tag/v1.0.0
