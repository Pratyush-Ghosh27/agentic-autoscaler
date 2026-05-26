# Agentic Autoscaler v2 — Implementation Strategy

**Date:** 2026-05-26
**Status:** Draft for team review
**Companion to:**
- `docs/design_v2.md` — current-state v2 system specification (`a08bbf4f`)
- `docs/v2_revision notes.md` — historical record of audit passes (F1–F40 + F2a-revisited)
- `docs/gap-report-v2.md` — code-vs-spec gap analysis (G10–G23)
- `docs/v2-spec-revision-plan.md` — pending decisions (D1–D13) and pending spec edits (E1–E23)
- `docs/superpowers/specs/2026-05-24-agentic-autoscaler-implementation-strategy.md` — v1 strategy (kept for reference; v1 plans 01–11 are closed)

---

## 1. Purpose

This strategy locks in the path from "v2 spec drafted, code partially aligned" to "v2 spec final, code in lockstep, banner flipped to Approved." It supersedes neither the design nor the gap report; it sequences them.

Inputs:

- **`design_v2.md`** as the source of truth for current-state behaviour.
- **`gap-report-v2.md`** as the enumerated code work (G10–G23, plus rolled-forward v1 closures).
- **`v2-spec-revision-plan.md`** as the enumerated spec work (D1–D13 decisions, E1–E23 edits, plus a full F1–F40 ledger).

Output: a six-phase sequence that touches every F-finding, every G-gap, and every D/E item, in dependency order, with TDD discipline carried over from the v1 strategy.

This strategy does not modify the design spec inline. Spec edits are landed via Phase 1 (and Phases 2/5/6 for the code-gated trailers); code edits are landed via Phases 2–5; the final banner promotion is Phase 6.

---

## 2. Decisions ratified

The thirteen pending decisions in `v2-spec-revision-plan.md` §1 are ratified here per the recommendations in that document. Each becomes an input constant for downstream phases.

| ID | Decision | Ratified value |
| --- | --- | --- |
| D1 | `K_TOD_DOWN` ↔ `K_PERIODIC_DOWN` divergence | (a) Rename in code to `KPeriodicDown`; spec disclaimer comes out once rename lands. |
| D2 | snake_case ↔ PascalCase Reason mapping table | (b) Condense to one-liner once G22 lands. Interim: keep formatted table (E13). |
| D3 | Revisit F7 + F14 (`peakP95` robustness) | (a) Re-affirm both verdicts; no spec change. |
| D4 | Revisit F9 (forecaster-swap discontinuity) | (b) Add §7 boundary-stability paragraph. |
| D5 | `autoscaling.agentic.io/skip-context` annotation | (b) Add the annotation. |
| D6 | Migration guide v1 → v2 | (b) Sibling doc `docs/migrating-v1-to-v2.md`. |
| D7 | Glossary section | (a) New §11 Glossary in `design_v2.md`. |
| D8 | Acceptance criteria section | (a) New §12 Acceptance criteria in `design_v2.md`. |
| D9 | §9 failure-mode coverage | (a) Audit and add 3–5 new rows. |
| D10 | Versioning banner | (c) Promote to "Approved" once both spec is internally consistent and code matches. |
| D11 | Promote F22 (`auto` never picks `gbdt_quantile`) into spec body | (a) Add §5 auto-mode subsection. |
| D12 | `LINEAR_EXTRAP_TREND_BLEND` polarity | (c) Rename to `LINEAR_EXTRAP_RECENT_WEIGHT`, default `0.7`, formula: `m_blended = LINEAR_EXTRAP_RECENT_WEIGHT * m + (1 - LINEAR_EXTRAP_RECENT_WEIGHT) * trend_24h_slope`. |
| D13 | Trigger numbering "three" vs "four" | (a) Split §6.1 "Initial trigger" from "Re-classification triggers". |

These are the canonical answers; if any need to change, a separate decision-record entry must edit this section first, then propagate downstream.

---

## 3. Phase enumeration

The work is partitioned into six phases. Phase boundaries are chosen so each phase produces a working, testable system on its own — the boundary between phases is exactly where new tests can pass independently of the next phase.

### Phase 1 — Spec edits (docs only)

**Plan:** `docs/superpowers/plans/2026-05-26-plan-12-v2-spec-edits.md`
**Goal:** Land all unblocked and decision-gated spec edits (E4–E23 except E1/E2/E3/E11). Ratify D1–D13 by baking their outputs into `design_v2.md`. Create migration guide, glossary, and acceptance criteria sections.
**Items closed:** E4, E5, E6, E7, E8, E9, E10, E12, E13, E14, E15, E16, E17, E18, E19, E20, E21, E22, E23.
**F-findings addressed:** F7 (D3 affirmed), F8 (D5 annotation), F9 (D4 boundary paragraph), F16/F31 (D12 rename), F22 (D11 auto-mode subsection), F25 (E4 cross-ref verification), F40 (E6 token consistency).
**Acceptance:** `design_v2.md` passes internal consistency checks; all §-cross-references resolve; glossary covers all 15+ terms; acceptance criteria section lists 20+ testable assertions.
**Estimated effort:** 1.5 days.

### Phase 2 — Foundations (Go + Python schema wiring)

**Plan:** `docs/superpowers/plans/2026-05-26-plan-13-v2-foundations.md`
**Goal:** Wire the `Context` object end-to-end (CRD schema, controller adapter, Forecast Service Pydantic model). Implement cold-path context computation (cadence change to 5-min, new features, raised thresholds). Align env-var defaults.
**Gaps closed:** G10, G11, G21.
**F-findings addressed (code-side):** F2a-revisited (72/240 thresholds), F4a (autocorr lag), F13 (KPeriodicDown rename), F18 (rps/min units), F23 (RPS_PER_POD_NOISE_FLOOR_RPS env), F24 (GBDT_MIN_POINTS env), F26 (relative threshold), F28 (max(mean,1.0) denominator), F29 (CV_GUARD_MEAN_RPS named), F32c (env-tunable), F36 (FORECAST_HORIZON_MINUTES service-only).
**Spec-trailer edits landed alongside:** E1 (K_TOD_DOWN disclaimer removal), E3 (F2a-revisited prose tightening).
**Acceptance:** `make test` passes; CRD has `Context` subfields; classifier worker writes all 5 context fields on a 5-min-cadence Prometheus series; controller forwards context to `/recommend`; env vars from v2 §4 tables are all parseable.
**Estimated effort:** 4 days.

### Phase 3 — Forecaster surface (Python + Go)

**Plan:** `docs/superpowers/plans/2026-05-26-plan-14-v2-forecaster-surface.md`
**Goal:** Implement `gbdt_quantile` forecaster end-to-end. Fix Prophet anchoring + hourly regressor. Implement linear_extrap trend blend + intercept recompute + window env var. Switch classifier forecaster selector to pattern-driven.
**Gaps closed:** G12, G14, G15, G19.
**F-findings addressed (code-side):** F3a/F17 (Prophet ds anchor), F5 (PROPHET_MIN_POINTS=30), F6 (current_hour_utc validation), F16/F31 (linear blend + intercept recompute), F20 ring-buffer-indirectly (GBDT path adds env), F21 (GBDT timestamp anchor), F22 (auto never picks gbdt in code).
**Acceptance:** Each forecaster has unit tests asserting spec-mandated behavior; `/recommend?preferred_model=gbdt_quantile` returns valid predictions; auto mode provably never returns `gbdt_quantile`; classifier `spiky` pattern maps to `gbdt_quantile`.
**Estimated effort:** 4 days.

### Phase 4 — Operator visibility (Go)

**Plan:** `docs/superpowers/plans/2026-05-26-plan-15-v2-operator-visibility.md`
**Goal:** Surface `unboundedRecommended` + binding tokens (`MaxReplicasBinding`, `MinReplicasBinding`). Update ExplainWorker prompt with long-term context line, binding-token conditionals, and new `ExplainRequest` fields.
**Gaps closed:** G13, G18.
**F-findings addressed (code-side):** F12 (Pattern!="" gate in prompt), F27 (unboundedRecommended + binding tokens), F33 (prompt conditionals for binding tokens).
**Acceptance:** When forecast exceeds maxReplicas, event message contains `unboundedRecommended`; ExplainWorker prompt includes "Long-term context" line when Pattern is non-empty; binding-token prose includes CRD-bound value.
**Estimated effort:** 2 days.

### Phase 5 — Bug-fix sweep (Go + Python + manifests)

**Plan:** `docs/superpowers/plans/2026-05-26-plan-16-v2-bugfix-sweep.md`
**Goal:** Fix generation watcher (use revision annotation), ring-buffer 5-copy seed, webhook strict inequality + `gbdt_quantile` enum widening, K8s Event Reason PascalCase migration.
**Gaps closed:** G16, G17, G20, G22.
**F-findings addressed (code-side):** F19 (revision annotation watcher), F20 (5-copy seed), F37 (webhook strict inequality), F39 (PascalCase Reason).
**Spec-trailer edit landed alongside:** E2 (PascalCase mapping condensation — one-liner replaces 16-row table once G22 lands).
**Acceptance:** envtest proves `/scale` patch does NOT signal re-classification; ring buffer seeds 5 copies; webhook rejects `maxReplicas == minReplicas`; `kubectl get events` shows PascalCase Reason fields.
**Estimated effort:** 2 days.

### Phase 6 — Final verification and banner flip

**Plan:** `docs/superpowers/plans/2026-05-26-plan-17-v2-final-verification.md`
**Goal:** Full end-to-end verification pass. Confirm every acceptance criterion from `design_v2.md` §12 passes. Land E11 (banner: "Approved"). Update `v2_revision notes.md` with a Pass 7 closure entry.
**Items closed:** E11, D10 gate satisfied.
**Acceptance:** `make test` + nightly E2E both green; `design_v2.md` line 3 reads "Status: Approved"; gap-report-v2.md can be annotated "all gaps closed."
**Estimated effort:** 0.5 days.

**Total estimated effort:** ~14 days single-contributor, distributed across 6 plans.

---

## 4. Dependency graph

```
Phase 1 (spec edits)
    |
    v
Phase 2 (foundations: G10, G11, G21)  ──────────────────┐
    |                                                     |
    v                                                     v
Phase 3 (forecasters: G12, G14, G15, G19)    Phase 4 (visibility: G13, G18)
    |                                                     |
    └──────────────────┬──────────────────────────────────┘
                       v
               Phase 5 (bug-fix sweep: G16, G17, G20, G22)
                       |
                       v
               Phase 6 (banner flip + final verification)
```

Phase 3 and Phase 4 can run in parallel after Phase 2. Phase 5 depends on Phase 3 (G20's `gbdt_quantile` enum widening requires G12 to exist) and Phase 4 (G22 PascalCase migration touches the same event sites as G13's binding tokens). Phase 6 gates on all prior phases.

---

## 5. Traceability matrix (F1–F40)

Every finding maps to exactly one phase (or is already spec-done with no further action). This table is the definitive cross-reference.

| F# | Status | Phase | Item(s) |
| --- | --- | --- | --- |
| F1 | spec-done | — | forecast_hw_seasonal removed |
| F2a | superseded | — | replaced by F2a-revisited |
| F2a-rev | code gap | Phase 2 | G11 (thresholds 72/240) + E3 |
| F3a | code gap | Phase 3 | G14 (Prophet ds anchor) |
| F4a | code gap | Phase 2 | G11 (autocorr lag) |
| F5 | code gap | Phase 3 | G14/G21 (PROPHET_MIN_POINTS=30) |
| F6 | code gap | Phase 2/3 | G10/G14 (current_hour_utc validation) |
| F7 | affirmed | Phase 1 | D3 — no spec change |
| F8 | revisited | Phase 1 | D5 — skip-context annotation |
| F9 | revisited | Phase 1 | D4 — boundary-stability paragraph |
| F10 | spec-done | — | per-request clarification |
| F11 | spec-done | — | 264 example |
| F12 | code gap | Phase 4 | G18 (Pattern!="" prompt gate) |
| F13 | code gap | Phase 2 | G11/G19 (KPeriodicDown rename) + E1 |
| F14 | folded into D3 | Phase 1 | affirmed with F7 |
| F15 | spec-done | — | slope in arch diagram |
| F16 | code gap + spec | Phase 1 + 3 | D12/E20/E21/E22 + G15 |
| F17 | code gap | Phase 3 | G14 (current_minute_utc) |
| F18 | code gap | Phase 2 | G11 (rps/min units) |
| F19 | code gap | Phase 5 | G16 (revision annotation) |
| F20 | code gap | Phase 5 | G17 (5-copy seed) |
| F21 | code gap | Phase 3 | G12 (GBDT timestamp anchor) |
| F22 | spec + code | Phase 1 + 3 | D11/E19 + G12/G19 |
| F23 | code gap | Phase 2 | G21 (env var) |
| F24 | code gap | Phase 2 | G21 (env var) |
| F25 | spec edit | Phase 1 | E4 (cross-ref verification) |
| F26 | code gap | Phase 2 | G11 (relative threshold) |
| F27 | code gap | Phase 4 | G13 (unbounded + binding tokens) |
| F28 | code gap | Phase 2 | G11 (max(mean,1.0) denominator) |
| F29 | code gap | Phase 2 | G11 (CV_GUARD_MEAN_RPS) |
| F30 | spec-done | — | priority-rationale paragraph |
| F31 | code gap + spec | Phase 1 + 3 | D12/E20/E21/E22 + G15 |
| F32c | code gap | Phase 2 | G21 (env-tunable) |
| F33 | code gap | Phase 4 | G18 (prompt conditionals) |
| F34 | spec-done | — | precedence rule 4 |
| F35 | spec-done | — | status example arithmetic |
| F36 | code gap | Phase 2 | G21 (FORECAST_HORIZON_MINUTES service-only) |
| F37 | code gap | Phase 5 | G20 (webhook strict inequality) |
| F38 | spec-done | — | "clip" wording |
| F39 | code gap | Phase 5 | G22 (PascalCase Reason) + E2 |
| F40 | spec edit | Phase 1 | E6 (token consistency verification) |

**Tally:** 41 labels. 10 spec-done (no action). 12 addressed in Phase 1 (spec). 11 in Phase 2. 6 in Phase 3. 3 in Phase 4. 4 in Phase 5. Several span two phases (spec + code). Zero missing.

---

## 6. Gap-report traceability (G10–G23)

| Gap | Phase | Depends on |
| --- | --- | --- |
| G10 | Phase 2 | — |
| G11 | Phase 2 | — |
| G12 | Phase 3 | G10 (context wiring) |
| G13 | Phase 4 | G10 (status schema) |
| G14 | Phase 3 | G10 (context forwarding) |
| G15 | Phase 3 | G10 (context.trend_24h_slope), D12 polarity (Phase 1) |
| G16 | Phase 5 | — (independent) |
| G17 | Phase 5 | — (independent) |
| G18 | Phase 4 | G10 (context fields), G13 (binding tokens) |
| G19 | Phase 3 | G12 (gbdt_quantile exists) |
| G20 | Phase 5 | G12 (enum includes gbdt_quantile) |
| G21 | Phase 2 | — |
| G22 | Phase 5 | — (cosmetic) |
| G23 | — | Doc-only; no code. Addressed by Phase 1 where applicable. |

---

## 7. Risk register

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| D12 polarity choice breaks existing operator muscle memory | Low | Med | `LINEAR_EXTRAP_RECENT_WEIGHT` name is self-documenting; migration guide (E7) calls it out explicitly. |
| GBDT quantile regressor (G12) overfits on short history | Med | Med | `GBDT_MIN_POINTS=30` guard + safety cap at `peak_p95_rps * 3`; nightly E2E with spiky scenario. |
| Prophet hourly regressor makes predictions worse when profile is noisy | Med | Low | `PROPHET_USE_HOURLY_REGRESSOR` env toggle allows disabling; `hourlyProfileValid` gate ensures 12+ hours of coverage. |
| Phase 2 cadence change (1-min to 5-min) breaks existing classifier tests | High | Low | Tests are rebaselined in Phase 2; old fixtures replaced with 5-min-cadence equivalents. |
| Phase 5 PascalCase migration breaks existing Grafana dashboards/alerts | Med | Med | Migration guide (E7) documents the Reason-field rename; Phase 5 plan includes a grep audit of deploy manifests. |

---

## 8. Out of scope

Carried forward from `v2-spec-revision-plan.md` §4:

- Operational-defaults recalibration (needs production data)
- Renumbering F1–F40 (breaks back-references)
- Folding revision notes back into design_v2.md (regression)
- Cross-version CRD migration (no v1beta1 planned)
- v1 plan re-execution (all v1 gaps closed)

---

## 9. Execution model

Same as v1: each phase has its own plan file (numbered 12–17, continuing from v1's plans 01–11). Plans are executed via:

- **Subagent-Driven Development** (recommended) — fresh subagent per task, review between tasks.
- **Inline Execution** — batch execution with checkpoints.

Phase 1 plan is drafted in full alongside this strategy (see `plan-12`). Phases 2–6 plans are outlined here and will be expanded into full bite-sized-task plans when their predecessor phase completes (so the file reads are fresh and the code edits reflect actual state).

---

## 10. Self-review checklist

- [x] Every F1–F40 finding appears in §5 traceability matrix
- [x] Every G10–G23 gap appears in §6 gap traceability
- [x] Every D1–D13 decision is ratified in §2
- [x] Every E1–E23 edit maps to a phase (E1/E3 in Phase 2; E2 in Phase 5; E4–E23 in Phase 1; E11 in Phase 6)
- [x] Dependency graph in §4 matches the "Depends on" column in §6
- [x] Estimated effort sums to ~14 days (consistent with gap-report-v2.md's 10–12 day code estimate plus 1.5–2 days spec work)
- [x] No circular dependencies between phases

