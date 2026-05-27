# v2 Acceptance Criteria — Coverage Matrix

**Date:** 2026-05-27
**Spec under audit:** `docs/design_v2.md` §11 (acceptance criteria, 26 entries)
**Phase:** Phase 6 final verification (Plan 17)
**Bottom line:** Every criterion in §11 is pinned by a passing automated test. The citations below are the evidence supporting D10's "code matches spec" gate.

This matrix is the input for the Phase 6 banner flip (E11). It is not regenerated automatically; refresh it whenever §11 changes.

---

## Coverage table

| # | Criterion | Pinning test | Notes |
| --- | --- | --- | --- |
| 1 | CRD `spec.preferredForecaster` enum accepts the four values | `internal/webhook/v1alpha1/validator_test.go::TestValidateSpec_AcceptsKnownForecasters` (subtests: `prophet`, `linear_extrap`, `gbdt_quantile`, `auto`) | Phase 3 (G12/G20) |
| 2 | Webhook rejects `maxReplicas <= minReplicas` (strict) | `internal/webhook/v1alpha1/validator_test.go::TestValidateSpec_RejectsMinEqualsMax` | Phase 5 (G20/F37) |
| 3 | `status.classifiedParams.context` populated with all 5 fields | `internal/classifier/worker_test.go::TestWorker_WritesContextToStatus` | Phase 2 (G10/G11) |
| 4 | Controller forwards `context` to `/recommend` when present | `internal/controller/agenticautoscaler_controller_test.go` envtest "forwards classifier context to the Forecast Service when present (G10)" | Phase 2 (G10) |
| 5 | Controller omits `context` when `classifiedParams` is nil | `internal/controller/agenticautoscaler_controller_test.go` envtest "omits context when classifiedParams is nil" | Phase 2 (G10) |
| 6 | `skip-context` annotation forces omit unconditionally | `internal/controller/agenticautoscaler_controller_test.go` envtest covering `AnnotationSkipContext` | Phase 2 (G10) |
| 7 | Forecast Service `/recommend` accepts and validates `context` | `forecast-service/tests/integration/test_app.py::test_recommend_endpoint_accepts_context_block` | Phase 2 (G10) |
| 8 | `spiky` + `auto` mode returns `model_used != "gbdt_quantile"` | `forecast-service/tests/unit/test_dispatch.py` (F22 invariant test) | Phase 3 (G12/G19) |
| 9 | `spiky` + `preferredForecaster: gbdt_quantile` returns `gbdt_quantile` | `forecast-service/tests/integration/test_app.py::test_recommend_endpoint_routes_gbdt_quantile_when_preferred` | Phase 3 (G12) |
| 10 | Prophet `ds[-1]` anchored to context UTC hour+minute | `forecast-service/tests/unit/test_prophet_model.py` (anchoring test) | Phase 3 (G14/F3a/F17) |
| 11 | Prophet adds `hour_baseline` regressor when profile valid | `forecast-service/tests/unit/test_prophet_model.py` (regressor test) | Phase 3 (G14) |
| 12 | `linear_extrap` blends slope and recomputes intercept | `forecast-service/tests/unit/test_linear_extrap.py` (G15 blend + intercept tests) | Phase 3 (G15/F16/F31) |
| 13 | `linear_extrap` clips at `peak_p95_rps * 1.5` | `forecast-service/tests/unit/test_linear_extrap.py` (T8 clip test) | Phase 3 (G15) |
| 14 | `max_replicas_binding` event includes `unboundedRecommended` | `internal/controller/agenticautoscaler_controller_test.go` G13 envtest "persists UnboundedRecommended into status and emits MaxReplicasBinding event" | Phase 4 (G13/F27) |
| 15 | ExplainWorker prompt has `Long-term context:` line | `internal/explainer/prompt_test.go` (Long-term-context line tests) | Phase 4 (G18/F12) |
| 16 | ExplainWorker prompt has binding-constraint line for `*_binding` tokens | `internal/explainer/prompt_test.go` (binding-token conditional tests) | Phase 4 (G18/F33) |
| 17 | `/scale` patch does NOT trigger reclassify (revision annotation watcher) | `internal/controller/agenticautoscaler_controller_test.go` G16 envtest "does NOT update last-observed revision when /scale bumps generation" | Phase 5 (G16/F19) |
| 18 | Ring buffer seeds 5 copies on restart | `internal/decision/state_test.go::TestRingBuffer_SeedN_PreservesMedianAcrossFreshObservations` + `internal/decision/decision_test.go::TestInitializeState_FromStatus` | Phase 5 (G17/F20) |
| 19 | K8s Event `Reason` is PascalCase, body has snake_case | `internal/reasoning/tokens_test.go::TestPascalReason_AllTokensHaveMapping` + `TestPascalReason_SpecificMappings` | Phase 5 (G22/F39) |
| 20 | Classifier query uses 5-min step; `L=12` autocorr lag with `< L+10` guard | `internal/classifier/features_test.go` (autocorr lag tests) | Phase 2 (G11/F4a) |
| 21 | `gradual_ramp` rule fires on relative-drift threshold | `internal/classifier/classify_test.go` (gradual_ramp relative-threshold test) | Phase 2 (G11/F26) |
| 22 | `peak_to_trough` denominator is `max(mean, 1.0)` | `internal/classifier/features_test.go::TestPeakToTrough_UsesMaxMeanOneDenominator` | Phase 2 (G11/F28) |
| 23 | `CLASSIFIER_MIN_POINTS` defaults to 72; floor derives from `L+10` | `internal/config/config_test.go` (defaults + validation tests) | Phase 2 (G21) |
| 24 | All v2 env vars parseable on the right deployment | `internal/config/config_test.go` + `forecast-service/tests/unit/test_app.py` (env-parsing tests) | Phase 2 (G21) |
| 25 | Code constant is `KPeriodicDown` (no `KTodDown`) | `internal/classifier/params_test.go` (rename verification test) | Phase 3 (G19/F13) |
| 26 | `auto`-mode never returns `gbdt_quantile` (any history/classifier/context) | `forecast-service/tests/unit/test_dispatch.py` (F22 invariant test — same coverage as #8) | Phase 3 (G12/G19/F22) |

---

## Verification command

The full coverage is re-verifiable with:

```
make pre-flight
```

A green pre-flight is the formal evidence that all 26 criteria are satisfied today. This matrix is the human-readable index; the test commands are the source of truth.

---

## Out of scope

- Operational-defaults recalibration (pending real workload data — see strategy §8).
- Cross-version CRD migration (no v1beta1 planned).
- Acceptance criteria not yet in §11 (any future criteria are tracked in their own audit pass).
