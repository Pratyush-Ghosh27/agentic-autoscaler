# Plan 17 — v2 Final Verification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close v2. Produce a traceability matrix proving every acceptance criterion in `design_v2.md` §11 is pinned by a passing test, condense the now-redundant PascalCase mapping table (E2), annotate the audit trail (gap report + revision notes + strategy doc), and flip the spec banner from Draft to Approved (E11).

**Architecture:** Verification + documentation phase. Single PR. The plan is structured around the 26 acceptance criteria — each must be traceable to a passing test before E11 fires. No new code paths; no TDD red-green-refactor; the rigour comes from the audit producing evidence, not from new test cases. If T1's audit surfaces a criterion with no test, the plan stops and adds a TDD task.

**Tech Stack:** Existing Go + Python test infrastructure (`make pre-flight`, `go test`, `pytest`); markdown for the spec/strategy/audit-trail docs. No new dependencies.

**Phase context:** This is Phase 6 of the v2 implementation strategy (`docs/superpowers/specs/2026-05-26-v2-implementation-strategy.md`). Phases 1–5 are merged. The spec edits E1, E3 already landed (verified during plan-writing); E2 and E11 are the only remaining spec items. D10's gate ("Approved" banner needs both spec internal-consistency AND code-matches) is satisfied iff T1 produces a clean coverage matrix.

---

## File structure

| File | Type | Responsibility |
| --- | --- | --- |
| `docs/v2-acceptance-coverage.md` | Create | Traceability matrix — 26 rows, one per acceptance criterion, citing the test file:line that pins it. Persistent artifact for future audits. |
| `docs/design_v2.md` | Modify (2 sites) | E2 (lines 491-511 → one-liner); E11 (line 3 banner flip). |
| `docs/gap-report-v2.md` | Modify | Append a "Closure status" section with per-gap citation of the implementing plan. |
| `docs/v2_revision notes.md` | Modify | Append Pass 7 closure entry. |
| `docs/superpowers/specs/2026-05-26-v2-implementation-strategy.md` | Modify | Mark each phase header with its merged-on date. |

No code files are touched in this plan. If T1 surfaces an uncovered criterion, additional tasks are added inline before T2.

---

## Pre-audit summary (informational)

The coverage matrix below was pre-audited during plan-writing. T1 verifies each row by running the cited test. If any row fails, T1 stops and adds a follow-up TDD task. The matrix:

| # | Criterion (abbreviated) | Test file:line citation |
| --- | --- | --- |
| 1 | CRD enum has `gbdt_quantile` | `internal/webhook/v1alpha1/validator_test.go::TestValidateSpec_AcceptsKnownForecasters/gbdt_quantile` |
| 2 | webhook rejects `max <= min` | `internal/webhook/v1alpha1/validator_test.go::TestValidateSpec_RejectsMinEqualsMax` |
| 3 | `status.classifiedParams.context` populated with 5 fields | `internal/classifier/worker_test.go::TestWorker_WritesContextToStatus` |
| 4 | controller forwards `context` when present | `internal/controller/agenticautoscaler_controller_test.go` (G10 envtest "forwards classifier context to the Forecast Service when present") |
| 5 | controller omits `context` on cold start | `internal/controller/agenticautoscaler_controller_test.go` (G10 envtest "omits context when classifiedParams is nil") |
| 6 | `skip-context` annotation forces omit | `internal/controller/agenticautoscaler_controller_test.go` (skip-context annotation envtest) |
| 7 | Forecast Service `/recommend` accepts + validates `context` | `forecast-service/tests/integration/test_app.py::test_recommend_endpoint_accepts_context_block` |
| 8 | spiky + auto returns `!= gbdt_quantile` | `forecast-service/tests/unit/test_dispatch.py::test_dispatch_auto_never_routes_gbdt_quantile` (F22 invariant) |
| 9 | spiky + override returns `gbdt_quantile` | `forecast-service/tests/integration/test_app.py::test_recommend_endpoint_routes_gbdt_quantile_when_preferred` |
| 10 | Prophet `ds[-1]` anchored to context UTC hour+minute | `forecast-service/tests/unit/test_prophet_model.py::test_prophet_anchors_ds_to_context_hour_and_minute` |
| 11 | Prophet adds `hour_baseline` regressor | `forecast-service/tests/unit/test_prophet_model.py::test_prophet_uses_hourly_regressor_when_profile_valid` |
| 12 | linear blend + intercept recompute | `forecast-service/tests/unit/test_linear_extrap.py` (T6/T7 G15 tests) |
| 13 | linear clip at `peak_p95_rps * 1.5` | `forecast-service/tests/unit/test_linear_extrap.py:186` (T8 G15 test) |
| 14 | `max_replicas_binding` event has `unboundedRecommended` | `internal/controller/agenticautoscaler_controller_test.go` G13 envtest "persists UnboundedRecommended into status and emits MaxReplicasBinding event" |
| 15 | ExplainWorker prompt has `Long-term context:` line | `internal/explainer/prompt_test.go` (Long-term-context line tests, F12) |
| 16 | ExplainWorker prompt has binding-constraint line | `internal/explainer/prompt_test.go` (binding-token conditional tests, F33) |
| 17 | `/scale` does NOT trigger reclassify | `internal/controller/agenticautoscaler_controller_test.go` G16 envtest "does NOT update last-observed revision when /scale bumps generation" |
| 18 | ring buffer `Seed` writes 5 copies | `internal/decision/decision_test.go::TestInitializeState_FromStatus` + `internal/decision/state_test.go::TestRingBuffer_SeedN_*` |
| 19 | PascalCase Reason + snake_case body | `internal/reasoning/tokens_test.go::TestPascalReason_AllTokensHaveMapping` + `internal/reasoning/tokens_test.go::TestPascalReason_SpecificMappings` |
| 20 | 5-min step + `L=12` lag, `len < L+10` guard | `internal/classifier/features_test.go` (autocorr lag tests) |
| 21 | gradual_ramp relative threshold | `internal/classifier/classify_test.go` (gradual_ramp tests, F26) |
| 22 | peak_to_trough denominator `max(mean, 1.0)` | `internal/classifier/features_test.go::TestPeakToTrough_*` (F28) |
| 23 | `CLASSIFIER_MIN_POINTS=72` default | `internal/config/config_test.go` (defaults test) |
| 24 | env vars parseable on appropriate deployment | `internal/config/config_test.go` + `forecast-service/tests/unit/test_app.py` (env-parsing tests) |
| 25 | `KPeriodicDown` rename (no `KTodDown` left) | `internal/classifier/params_test.go` (KPeriodicDown rename test) |
| 26 | auto never returns `gbdt_quantile` | `forecast-service/tests/unit/test_dispatch.py::test_dispatch_auto_never_routes_gbdt_quantile` (same as #8) |

If a citation is stale (test renamed, file moved), T1 corrects the citation and re-runs.

---

## Tasks

### Task 1: Build the acceptance-criteria coverage matrix

**Files:**
- Create: `docs/v2-acceptance-coverage.md`

This task produces the deliverable that justifies E11's banner flip: every acceptance criterion in `design_v2.md` §11 must point to a passing test. Each cited test is run during this task; a failing or missing citation halts the plan and adds a follow-up TDD task before T2.

- [ ] **Step 1: Create the coverage matrix doc**

Write the following content to `docs/v2-acceptance-coverage.md`:

```markdown
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
```

- [ ] **Step 2: Verify the matrix's citations by running each cited test**

For each row in the matrix, run the cited test and confirm PASS. The pre-flight gate covers all of them in aggregate, but individual runs catch citation drift earlier than the aggregate fail. Suggested command sequence (one shell per package — feel free to batch with `-run` regexes):

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
XDG_CACHE_HOME=/home/pratyush.ghosh/scaler/.cache \
go test ./internal/webhook/v1alpha1/... -run 'TestValidateSpec_AcceptsKnownForecasters|TestValidateSpec_RejectsMinEqualsMax' -v -count=1

TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
XDG_CACHE_HOME=/home/pratyush.ghosh/scaler/.cache \
go test ./internal/classifier/... -run 'TestWorker_WritesContextToStatus|TestPeakToTrough|Autocorr|GradualRamp|KPeriodicDown' -v -count=1

TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
XDG_CACHE_HOME=/home/pratyush.ghosh/scaler/.cache \
go test ./internal/decision/... -run 'TestRingBuffer_SeedN|TestInitializeState_FromStatus' -v -count=1

TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
XDG_CACHE_HOME=/home/pratyush.ghosh/scaler/.cache \
go test ./internal/reasoning/... -run 'TestPascalReason' -v -count=1

TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
XDG_CACHE_HOME=/home/pratyush.ghosh/scaler/.cache \
go test ./internal/explainer/... -run 'Prompt|LongTermContext|Binding' -v -count=1

cd forecast-service && pytest tests/ -k 'context or gbdt_quantile or anchor or hourly_regressor or peak_p95 or auto_never' -v
```

Expected: every cited test PASS. If any test is missing or fails:
- **Missing**: locate the actual test that pins the criterion (the citation is stale — fix the matrix row to point at the real test) **OR** add a new TDD task to this plan before T2.
- **Failing**: stop the plan and investigate. Failing pin-tests on `main` indicate a regression or a misclassified criterion.

- [ ] **Step 3: Run the full pre-flight suite**

```
make pre-flight
```

Expected: PASS — including lint, codegen-check, Go unit, Python unit + integration, and envtest suites.

- [ ] **Step 4: Commit the coverage matrix**

```
git add docs/v2-acceptance-coverage.md
git commit -m "docs: add v2 acceptance-criteria coverage matrix (Phase 6 T1)

Maps every criterion in design_v2.md §11 to a passing test. Output
of the Phase 6 audit; input for the E11 banner flip."
```

---

### Task 2: E2 — condense the §5 step-10 PascalCase mapping table

**Files:**
- Modify: `docs/design_v2.md` lines 491-511 (the `K8s Event Reason field naming` block)

The 16-row mapping table at lines 491-511 was added in Pass 6 (F39) when the Reason field was still snake_case in code. Phase 5 (G22) landed `reasoning.PascalReason` as the source of truth. Per D2 = (b), the spec should now carry only the principle; the per-token enumeration lives in `internal/reasoning/tokens.go` and is pinned by `internal/reasoning/tokens_test.go`.

- [ ] **Step 1: Replace the table with the one-liner**

In `docs/design_v2.md`, find the block starting with the bullet:

```
    * K8s Event `Reason` field naming: the snake_case reasoning tokens are the canonical identifiers; the actual K8s Event `Reason` field uses their PascalCase equivalent. The full mapping (covering Reconciler, ClassifierWorker, and ExplainWorker tokens):
```

Replace that bullet AND the 17-line markdown table that follows AND the trailing `The snake_case token is included verbatim...` sentence (entire block at lines 491-511) with this single bullet:

```
    * K8s Event `Reason` field naming: the snake_case reasoning tokens are the canonical identifiers and appear verbatim in the message body for log searchability; the K8s Event `Reason` field carries the PascalCase equivalent per K8s convention (e.g., `scale_up` → `ScaleUp`, `max_replicas_binding` → `MaxReplicasBinding`). The full per-token mapping is implemented in `internal/reasoning/tokens.go` (`PascalReason` function) and pinned by `internal/reasoning/tokens_test.go::TestPascalReason_AllTokensHaveMapping` so the table cannot drift from code.
```

- [ ] **Step 2: Verify the §5 step 10 list still renders correctly**

The condensation removes ~20 lines but the surrounding bullets (`scale_up / scale_down / no_change`, `step_capped_*`, `cooldown_holding_*`, `max_replicas_binding / min_replicas_binding`, `kill_switched`, `conflict_detected`, `forecast_unavailable`, `metrics_unavailable`) must remain — those describe **when** each token fires, which is design content, not a mapping table.

Re-read the modified §5 step 10 in `docs/design_v2.md` and confirm the structure:

1. The step-10 numbered preamble ("Emit a K8s Event with a reasoning token:") is intact.
2. Each `*` bullet listing snake_case tokens with their firing condition is intact.
3. The new condensed bullet about PascalCase ↔ snake_case is the LAST bullet of step 10.

If any of the three bullets above were lost, restore them from `git show HEAD~:docs/design_v2.md` and redo step 1.

- [ ] **Step 3: Run pre-flight to confirm no codegen or doc-link breaks**

```
make pre-flight
```

Expected: PASS. (Markdown links don't get codegen-checked, but pre-flight catches any accidental changes to other files; this is a sanity check.)

- [ ] **Step 4: Commit**

```
git add docs/design_v2.md
git commit -m "docs(design_v2): condense PascalCase Reason mapping to one-liner (E2)

Removes the 16-row snake_case ↔ PascalCase table at §5 step 10 now
that reasoning.PascalReason is the source of truth. The principle
(PascalCase Reason field, snake_case in body) stays in the spec; the
per-token enumeration lives in code and is pinned by
TestPascalReason_AllTokensHaveMapping. D2 = (b)."
```

---

### Task 3: Annotate `gap-report-v2.md` with closure status

**Files:**
- Modify: `docs/gap-report-v2.md` (append a new §5 + per-gap header annotations; do NOT delete original gap descriptions — the report is an audit artifact and stays append-only)

The current report enumerates G10–G23 as open. Phase 6 needs to record which plan closed each gap so future readers can trace any G# back to its implementing PR.

- [ ] **Step 1: Append a new §5 "Closure status" section**

Append the following block to the end of `docs/gap-report-v2.md` (after the existing §4 "Recommended v2 sequence"):

```markdown

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
```

- [ ] **Step 2: Commit**

```
git add docs/gap-report-v2.md
git commit -m "docs(gap-report-v2): annotate closure status for G10-G22 (Phase 6 T3)

Adds a §5 'Closure status' section mapping each gap to the plan that
closed it. The original gap descriptions (§1-4) remain unchanged; the
report is append-only as an audit artifact."
```

---

### Task 4: Append Pass 7 closure entry to `v2_revision notes.md`

**Files:**
- Modify: `docs/v2_revision notes.md` — append a new bullet at the end

The existing file has Passes 1–6 (findings F1–F40) plus the Pass 7 entry for D12 (`LINEAR_EXTRAP_RECENT_WEIGHT` rename). Phase 6 lands E2 (mapping condensation) and E11 (banner flip), which are the final spec-side residuals from the v2 audit. The Pass 7 entry should record both.

- [ ] **Step 1: Append the closure entry**

At the end of `docs/v2_revision notes.md`, after the existing Pass 7 D12 paragraph (the one starting with `**D12 resolution (spec-rename trailer for F16 / F31)**`), append the following two bullets (matching the existing `>` blockquote style):

```markdown
>   * **E2 resolution (PascalCase mapping condensation, 2026-05-27)**: with `internal/reasoning/PascalReason` landed in Plan 16 (Phase 5, G22/F39), the `design_v2.md` §5 step 10 mapping table is now redundant — code is the source of truth for snake_case ↔ PascalCase. Condensed the table to a one-liner pointing operators at `internal/reasoning/tokens.go` and the `TestPascalReason_AllTokensHaveMapping` pin. D2 = (b).
>   * **E11 resolution (banner flip, 2026-05-27)**: D10's "Approved" gate is satisfied — both `design_v2.md` is internally consistent (Phase 1 closed E4–E23 except E1/E2/E3/E11; E1, E2, E3, E11 land in Phases 2/5/6) and the code matches (`docs/v2-acceptance-coverage.md` produced in Phase 6 cites a passing test for every criterion in §11). Flipped `design_v2.md` line 3 from "Status: Draft for team review" to "Status: Approved" and bumped the date to 2026-05-27. v2 is closed; future revisions land via a new audit pass (and would create a `v2.1_revision notes.md` rather than reopening this file).
```

- [ ] **Step 2: Commit**

```
git add "docs/v2_revision notes.md"
git commit -m "docs: append Pass 7 closure entries E2 + E11 (Phase 6 T4)

E2 records the PascalCase mapping condensation (table → one-liner now
that code is the source of truth). E11 records the banner flip from
Draft to Approved, with the D10 gate evidence (per-criterion coverage
matrix in docs/v2-acceptance-coverage.md)."
```

---

### Task 5: Mark all 6 phases complete in the strategy doc

**Files:**
- Modify: `docs/superpowers/specs/2026-05-26-v2-implementation-strategy.md` (annotate each phase header in §3)

The strategy doc enumerates 6 phases with status implicit. Phase 6 is the closure phase; before E11 fires, the strategy should explicitly mark each phase as merged with its date. The annotation enables future readers to skim §3 and immediately see the v2 timeline.

- [ ] **Step 1: Annotate each phase header**

In `docs/superpowers/specs/2026-05-26-v2-implementation-strategy.md` §3, append a `**Status:** completed YYYY-MM-DD (PR: <plan ref>)` line as the LAST line of each phase's block (immediately before the next `### Phase N` header — or before the `## 4. Dependency graph` section for Phase 6).

Use these exact lines (one per phase):

For Phase 1:
```
**Status:** completed 2026-05-26 (Plan 12 — `docs/superpowers/plans/2026-05-26-plan-12-v2-spec-edits.md`).
```

For Phase 2:
```
**Status:** completed 2026-05-26 (Plan 13 — `docs/superpowers/plans/2026-05-26-plan-13-v2-foundations.md`).
```

For Phase 3:
```
**Status:** completed 2026-05-26 (Plan 14 — `docs/superpowers/plans/2026-05-26-plan-14-v2-forecaster-surface.md`).
```

For Phase 4:
```
**Status:** completed 2026-05-27 (Plan 15 — `docs/superpowers/plans/2026-05-27-plan-15-v2-operator-visibility.md`).
```

For Phase 5:
```
**Status:** completed 2026-05-27 (Plan 16 — `docs/superpowers/plans/2026-05-27-plan-16-v2-bugfix-sweep.md`).
```

For Phase 6:
```
**Status:** completed 2026-05-27 (Plan 17 — `docs/superpowers/plans/2026-05-27-plan-17-v2-final-verification.md`). All v2 work is closed.
```

If any of the listed phases shows a different actual completion date in `git log` (the date of the merge commit on `main`), use the actual date instead of the placeholder above.

- [ ] **Step 2: Update §10 self-review checklist**

In §10 of the same file, append a new checked item to the existing list:

```
- [x] All 6 phases marked complete with their merge-dates and plan references (Phase 6 T5)
```

- [ ] **Step 3: Commit**

```
git add docs/superpowers/specs/2026-05-26-v2-implementation-strategy.md
git commit -m "docs(strategy): mark all 6 v2 phases complete (Phase 6 T5)"
```

---

### Task 6: E11 — flip the `design_v2.md` banner from Draft to Approved

**Files:**
- Modify: `docs/design_v2.md` line 3 (the only edit)

**Pre-condition gate (verify BEFORE editing):**
- T1's coverage matrix is committed and every cited test passes (`make pre-flight` was green at the end of T1).
- T2 (E2 condensation), T3 (gap-report annotation), T4 (Pass 7 entry), T5 (strategy doc) are committed.

If any of those is not true, stop. E11 is the LAST commit in this PR by design — per `v2-spec-revision-plan.md` §3 ("Final gate: D10 + all §1/§2 complete + G10–G23 closed → E11"). Landing E11 with any prior task incomplete violates the gate.

- [ ] **Step 1: Verify the pre-condition**

Run:

```
git log --oneline main..HEAD
```

Expected: at least 5 commits on top of `main` corresponding to T1–T5 (matrix + E2 + gap-report annotation + Pass 7 entry + strategy doc). If fewer, return to the missing task.

- [ ] **Step 2: Replace the banner line**

In `docs/design_v2.md` line 3, replace:

```
Date: 2026-05-25 Status: Draft for team review (v2 — complete spec; supersedes v1 dated 2026-05-21)
```

with:

```
Date: 2026-05-27 Status: Approved (v2 — complete spec; supersedes v1 dated 2026-05-21)
```

(The "supersedes v1 dated 2026-05-21" suffix stays — it documents the v2-vs-v1 lineage and remains accurate.)

- [ ] **Step 3: Run pre-flight one more time**

```
make pre-flight
```

Expected: PASS. This is the formal close-out gate; if pre-flight is not green, the banner flip is invalid.

- [ ] **Step 4: Commit**

```
git add docs/design_v2.md
git commit -m "docs(design_v2): flip spec banner to Approved (E11 — D10 gate satisfied)

Closes v2. Pre-conditions satisfied:
- All 26 §11 acceptance criteria pinned by passing tests
  (see docs/v2-acceptance-coverage.md, Phase 6 T1).
- All G10-G22 gaps closed (see gap-report-v2.md §5).
- All E# spec edits landed (E1 in Phase 2, E2 in Phase 6 T2,
  E3 in Phase 2, E4-E10/E12-E23 in Phase 1, E11 here).

Future v2.x revisions create a new audit pass; this banner change
is irreversible without an explicit decision-record entry that
demotes it."
```

---

### Task 7: Final pre-flight + nightly E2E gate

This task produces no commits — it is the verification gate before pushing the PR. The plan terminates after this task.

- [ ] **Step 1: Confirm `make pre-flight` is green on the head of the branch**

```
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
XDG_CACHE_HOME=/home/pratyush.ghosh/scaler/.cache \
GOLANGCI_LINT_CACHE=/home/pratyush.ghosh/scaler/.cache/golangci \
make pre-flight
```

Expected: PASS for all 5 stages (lint, generate-check, test-go, test-python, test-envtest).

If pre-flight fails, **do not push**. Investigate, fix, and amend or add a fixup commit. The failure mode here is most likely a stale doc citation (a test renamed between T1's commit and HEAD); the fix is to re-audit the matrix.

- [ ] **Step 2: Run the nightly k6 ramp suite — out-of-band gate**

The nightly E2E exercises a real cluster (kind / Docker required). Two acceptance paths:

**Path A — local run (preferred):**

```
make nightly-e2e
```

Expected: green; specifically, `k6` reports no `http_req_failed` threshold breach and the run completes within the 3600s timeout.

If the local environment has Docker + kind available, use this path.

**Path B — CI delegation:**

If the local environment cannot run the nightly (no Docker, no kind, sandbox restrictions), satisfy the gate with:

1. The most recent CI nightly run on `main` (post-Phase 5 merge) was green. Verify by reviewing the nightly job output in GitHub Actions.
2. The PR's CI run (post-push) passes pre-flight.

Document which path was used in the PR description.

- [ ] **Step 3: Push the branch and open the PR**

```
git push -u origin feat/v2-final-verification
```

Then open the PR per the project's standard process. Include in the PR body:

1. A summary of what closed (E2, E11, audit-trail updates, coverage matrix).
2. Which Path (A or B) satisfied the nightly E2E gate.
3. The `git log --oneline main..HEAD` output as the commit summary.

- [ ] **Step 4: After merge — no further action**

Plan 17 is the last v2 plan. After this PR merges:
- v2 is closed.
- `gap-report-v2.md`'s closure footer reflects reality.
- `design_v2.md` reads "Status: Approved" on `main`.
- Any further v2-related work that emerges starts a new audit pass and produces a new revision-notes file (`v2.1_revision notes.md` or similar).

No follow-up tasks; no `Phase 7`.

---

## Self-review checklist

Run this checklist as a final gate before pushing.

- [x] Every criterion in `design_v2.md` §11 (1-26) appears in the coverage matrix in T1's pre-audit summary
- [x] T2's edit removes the 16-row mapping table (E2) and replaces it with the principle one-liner
- [x] T3's gap-report annotation cites the closing plan for each G# (G10-G23)
- [x] T4's Pass 7 entry covers BOTH E2 and E11 with rationale
- [x] T5's strategy doc annotation has all 6 phases marked complete
- [x] T6 is gated on T1-T5 being committed (the gate-check is step 1 of T6)
- [x] T7's final pre-flight is the formal CI parity check
- [x] No `TODO` / `TBD` / `fill in details` / `add tests for the above` placeholders
- [x] Every step is bite-sized (one action, ~2-5 minutes execution time)
- [x] Every code/doc step shows the literal text being added or removed
- [x] Type/name consistency: `PascalReason`, `KPeriodicDown`, `SeedN`, `unboundedRecommended` — same names used here as in their implementing plans (Plans 16 and 15 respectively)

---

## Post-merge cleanup

After the PR merges, the only file to delete is the temporary PR-body file (if one was created — Phase 6 likely doesn't need one given the small scope). No code or doc files need cleanup.

The plan files (Plans 12-17) and the artifacts they reference (`gap-report-v2.md`, `v2_revision notes.md`, `v2-spec-revision-plan.md`, `v2-acceptance-coverage.md`) are all archival and remain on `main` indefinitely.
