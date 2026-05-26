# v2 Spec Revision Plan

**Date:** 2026-05-26
**Spec under review:** `docs/design_v2.md` at `a08bbf4f` (pass-6 reviewer fixes + revision-notes split)
**Companions:**
- `docs/gap-report-v2.md` — code-vs-spec gaps (G10–G23). Forward-looking; drives the v2 implementation plan.
- `docs/v2_revision notes.md` — historical record of audit passes (F1–F40 + F2a-revisited). Past-tense; append-only.

This artifact captures the **spec-side** residue of the v2 audit: open decisions whose answers will produce spec edits, and concrete spec edits that are queued (some unblocked, some triggered by code changes landing). It is the input document for the v2 spec-revision PR sequence.

The body (§1, §2) is action-focused — only items still requiring work. The appendix (§5) is a full F1–F40 ledger so any reviewer can verify in one pass that every Pass 1–6 finding has been seen and classified.

---

## §1. Pending decisions

Each decision is a yes/no (or A/B/C) gate that, once answered, produces a concrete spec edit (see §2). Recommendations are mine; final calls are yours.

### D1. `K_TOD_DOWN` ↔ `K_PERIODIC_DOWN` naming divergence

**Background.** Pass 2 F13 renamed the cooldown constant from `K_TOD_DOWN` (leftover from when the feature was called `tod_correlation`) to `K_PERIODIC_DOWN` in the spec. The Go source at `internal/classifier/params.go:19` still uses `KTodDown`. design_v2.md:1060 carries an explicit disclaimer: *"In the source this constant is currently named `K_TOD_DOWN` … Spec uses the new name; rename in code is tracked separately."*

**Options.**
- (a) Rename in code (`internal/classifier/params.go` — `KTodDown → KPeriodicDown`). Lands as part of `gap-report-v2.md` G11 (cold-path overhaul) or G19 (forecaster selector). Disclaimer in §7 constants table comes out.
- (b) Rename spec back to `K_TOD_DOWN`. Loses the naming consistency with `hourly_autocorr` (renamed from `tod_correlation` in Pass 2) and the user-visible pattern label `periodic`.
- (c) Keep the disclaimer permanently. Honest about the divergence; no code churn; but every future audit pass has to re-confirm the naming asymmetry.

**Recommendation.** (a). The spec rename is the right one — `K_TOD_DOWN` is a stale name from a feature that no longer exists. Code should follow.

**Triggers.** Unblocked by G11 or G19 landing. Trigger spec edit **E1**.

---

### D2. snake_case ↔ PascalCase Event `Reason` mapping table

**Background.** Pass 6 F39 added a 16-row mapping at design_v2.md:484 because Go code emits the snake_case form (`scale_up`, `step_capped_up`) directly into the K8s Event `Reason` field, violating K8s convention. `gap-report-v2.md` G22 captures the code-side work to switch to PascalCase. Once code is the source of truth, the spec mapping table becomes redundant.

**Options.**
- (a) Keep the full table indefinitely as documentation — operators reading the spec see both forms in one place.
- (b) Condense to a one-liner once G22 lands: *"K8s Event `Reason` field uses PascalCase (e.g., `ScaleUp`); the snake_case token is included verbatim in the message body for log searchability."* Drop the per-token enumeration.
- (c) Delete the mapping entirely once G22 lands; rely on the constants in `internal/reasoning/tokens.go` as the source of truth.

**Recommendation.** (b). The principle (*"PascalCase in Reason, snake_case in body"*) belongs in the spec; the per-token enumeration belongs in code. A one-liner survives the next rename without dragging a 16-row table along.

**Triggers.** Unblocked by G22 landing. Trigger spec edit **E2**, **E13** (interim formatting until G22 lands).

---

### D3. Revisit F7 + F14 — `peakP95` robustness

**Background.** Pass 1 F7 considered switching `peakP95RPS` → `peakP90RPS` (less sensitive to single-spike outliers) and rebalancing per-forecaster safety-cap multipliers (1.5×/2×/2×/3×). Decided "no change". Pass 2 F14 raised the same concern from the opposite angle (small-sample p95 sensitivity at low MIN_POINTS) and was auto-resolved by F2a-revisited raising MIN_POINTS to 72 (p95 over 72 samples behaves as a real percentile, ~index 68). Both verdicts predate the current state of the spec.

**Options.**
- (a) Re-affirm both verdicts. p95 over 72 samples is robust; multipliers (1.5×/2×/2× — the spec dropped one when forecast_hw_seasonal was removed in F1) are reasonable. No spec change.
- (b) Switch to p90 anyway. Lower sensitivity to outliers; tighter safety-cap behaviour. Requires re-validating each forecaster's safety-cap multiplier against the new statistic.
- (c) Defer until empirical data exists from a v2 nightly run. Mark as "tune-later".

**Recommendation.** (a). The original objections were either weakened by F2a-revisited (small-sample sensitivity) or speculative (multiplier rebalancing). Without data, a desk decision is wrong. If a future operator-visible miss happens at p95 specifically, revisit then.

**Triggers.** No code dependency. (a) requires no spec edit. (b) requires re-validating each forecaster's safety-cap multiplier against the new statistic — non-trivial; would need its own audit pass before landing as a spec change.

---

### D4. Revisit F9 — forecaster-swap discontinuity

**Background.** Pass 1 F9 flagged that the previous v2 draft would silently swap `forecast_hw_seasonal → forecast_prophet` at the moment `hourlyProfileValid` flipped from `false` to `true`, with discontinuous prediction behaviour at the boundary. F1's removal of `forecast_hw_seasonal` "auto-resolved" this. But v2 still has classifier-driven forecaster swaps — e.g., a workload whose `cv` crosses 0.10 swaps from `flat → default` (both pick `linear_extrap`, so no swap), but a workload whose `hourly_autocorr` crosses 0.70 swaps from `default → periodic` (or `gradual_ramp → periodic`), changing the forecaster from `linear_extrap` (or `prophet`) to `prophet`. These are real swaps with potentially-discontinuous behaviour.

**Options.**
- (a) Affirm "no further action" — F1's auto-resolution was sufficient. Classifier-driven swaps happen on the cold-path tick (`CLASSIFIER_INTERVAL_MINUTES=30m`), so any discontinuity is bounded by that cadence and the operator-visible event (`pattern_classified`) makes the swap diagnosable.
- (b) Add a short "boundary stability" treatment to §7. One paragraph stating: when a feature crosses a classification threshold, the next classification cycle picks a new forecaster; the hot path then routes new requests to the new forecaster. The discontinuity is bounded by the classifier cadence and is operator-visible via the `pattern_classified` event. No special hysteresis logic.
- (c) Add classification-rule hysteresis (e.g., `cv < 0.08` to enter `flat`, `cv > 0.12` to leave). Adds spec surface and code complexity.

**Recommendation.** (b). The discontinuity is real but bounded; documenting it explicitly is cheaper than papering over it with hysteresis. Operators reading the spec should be able to predict swap-induced behaviour without reverse-engineering it from `pattern_classified` events.

**Triggers.** No code dependency. Trigger spec edit **E15** (new §7 paragraph).

---

### D5. `autoscaling.agentic.io/skip-context` annotation

**Background.** Pass 1 F8 deleted an illusory test path that suggested operators could "test without context" by manually clearing `status.classifiedParams.context`. The annotation never existed; the suggested workflow contradicted "ClassifierWorker owns `classifiedParams` exclusively". F8 chose to delete the misleading sentence rather than add a real annotation. v2's context surface is now substantially larger (5 fields × Forecast Service consumption × tests), and the operator value of "force the system into context-free mode for one reconcile" is correspondingly larger.

**Options.**
- (a) Re-affirm F8's "no annotation" — operators can edit `status.classifiedParams.context` directly with `kubectl patch` for ad-hoc testing. No spec change.
- (b) Add `autoscaling.agentic.io/skip-context: "true"` annotation. While set, the reconciler omits the `context` field in `/recommend` regardless of `status.classifiedParams.context`. Adds: §4 annotation table row, §5 step 3 conditional, §9 row (annotation-set behaviour). Low surface, real operational value.
- (c) Add the annotation but as `autoscaling.agentic.io/force-stale-context: "stale"` — keeps context but prevents classifier from refreshing it. Different use case (testing classifier independence). Out of scope for v2.

**Recommendation.** (b). The annotation is one §4 row and one §5 conditional; tests get materially easier with it. F8's "no annotation" verdict was right at Pass 1 (single field). v2's larger context surface tips the balance.

**Triggers.** No code dependency until implementation; spec edit can land first. Trigger spec edits **E16** (§4 annotation row), **E17** (§5 step 3 conditional), **E18** (§9 row).

---

### D6. Migration guide v1 → v2

**Background.** v2 widens the CRD enum (`gbdt_quantile`), adds the `Context` field to `ClassifiedParams`, adds two new reasoning tokens (`MaxReplicasBinding`, `MinReplicasBinding`), adds the `unboundedRecommended` status field, and changes several env-var defaults (`PROPHET_MIN_POINTS=60→30`, `CLASSIFIER_MIN_POINTS=70→72`). All are backward-compatible (additive at the API; default changes affect behaviour but not validity), but operators upgrading from v1 will want a single page that lists what changed.

**Options.**
- (a) Add a §10 "Migrating from v1" section to design_v2.md. Single page, lists CRD changes, env-var changes, behaviour changes, and a one-line "what to expect" per change.
- (b) Sibling doc at `docs/migrating-v1-to-v2.md`. Keeps design_v2.md focused on the current state; migration details don't bloat the spec for new users.
- (c) Nothing — operators read the diff between design_v2.md and design.md.

**Recommendation.** (b). The migration guide is read-once-during-upgrade, not part of the steady-state spec. Co-locating reduces clutter for the 95% of readers who are reading design_v2.md as a current-state reference.

**Triggers.** No code dependency. Trigger spec edit **E7** (new sibling doc).

---

### D7. Glossary section

**Background.** design_v2.md names ~15 specialised terms (`baseline_rps`, `peak_p95_rps`, `hourly_autocorr`, `trend_24h_slope`, `hourly_profile`, `peak_to_trough`, `cv`, `rps_per_pod`, `unboundedRecommended`, `recommendedReplicas`, `effectiveContext`, `effectiveForecaster`, `hourlyProfileValid`, `current_hour_utc`, `current_minute_utc`). Definitions are scattered across §4 / §6.1 / §7. A reader landing in §5 has to chase backwards.

**Options.**
- (a) Add §11 Glossary at the end of design_v2.md. Term → one-sentence definition → §-where-it's-defined link.
- (b) Sibling doc `docs/v2-glossary.md`.
- (c) No glossary; rely on inline definitions and search.

**Recommendation.** (a). Glossary belongs adjacent to the spec it serves. Sibling docs proliferate and stale silently. ~15 terms × 1 line each = half a page.

**Triggers.** No code dependency. Trigger spec edit **E8**.

---

### D8. Acceptance criteria / test-plan section

**Background.** design_v2.md doesn't declare what "v2 ships" means in terms of testable assertions. `gap-report-v2.md` G10–G23 implicitly enumerate the acceptance criteria, but the *spec* should declare its own ship gates so a future contributor doesn't infer them from the gap report alone.

**Options.**
- (a) Add §12 Acceptance criteria. Bulleted list: e.g., *"For a workload classified `spiky`, an unmodified `auto`-mode `/recommend` request returns `model_used == 'gbdt_quantile'` once `len(rps_history) >= GBDT_MIN_POINTS`"*; *"Prophet's `ds[-1]` aligns to `(context.current_hour_utc, context.current_minute_utc)` regardless of Forecast Service pod clock"*; etc.
- (b) Sibling doc `docs/v2-acceptance.md` with the same content.
- (c) Bake the criteria into §5 / §6.1 / §7 inline — *"behavior X must be testable as Y"* alongside each behavior.
- (d) No formal section — gap-report-v2.md plus the existing CI suite is enough.

**Recommendation.** (a). One section, ~20 bullets, gives the spec a clear ship gate. Inline (c) clutters the prose; sibling (b) splits the source of truth; (d) leaves intent unclear.

**Triggers.** No code dependency. Trigger spec edit **E9**.

---

### D9. §9 failure-mode table coverage

**Background.** §9 has 21 rows. The audit added rows for malformed `context`, hourly-profile under-coverage, GBDT failures, and Prophet failures. Possible gaps: webhook-rejection behaviour (probably out-of-scope for §9), `rpsPerPodCurrent` out-of-bounds at restart (covered indirectly by F20's 5-copy seed), `hourly_profile` array length ≠ 24 (covered by "malformed context" generic row but not explicitly), GBDT prediction returning negative or NaN (currently uncovered).

**Options.**
- (a) Audit §9 against every v2-introduced behaviour from §5 / §6.1 / §6.2 / §7; add rows for any uncovered failure path. Estimated 3–5 new rows.
- (b) Accept current coverage. Rely on the existing "malformed context: drop, log warning" generic row.
- (c) Restructure §9 by phase (hot-path failures, cold-path failures, Forecast Service failures, ExplainWorker failures) for readability while auditing for gaps.

**Recommendation.** (a). The audit identified specific likely gaps; closing them is mechanical. (c) is good but tangential to v2 — defer the restructure unless §9 keeps growing.

**Triggers.** No code dependency. Trigger spec edit **E10**.

---

### D10. Versioning banner

**Background.** Line 3 currently reads *"Status: Draft for team review (v2 — complete spec; supersedes v1 dated 2026-05-21)"*. A "draft" state with six audit passes is no longer obviously a draft. When the banner flips to "v2 — final" matters for downstream contributors who treat "draft" as "subject to change" and "final" as "needs an ADR to change".

**Options.**
- (a) Promote to "Approved" once D1–D9 are decided and the relevant E-edits land. Trigger: this artifact's E1–E11 reach completion (or explicit deferral).
- (b) Promote to "Approved" once code achieves spec-parity (G10–G23 close). Trigger: gap-report-v2.md becomes empty.
- (c) Promote to "Approved" once both (a) and (b) hold.
- (d) Keep "Draft" indefinitely; the spec evolves; no formal approval state.

**Recommendation.** (c). A spec is "approved" when it's both internally consistent (spec-side complete) and externally valid (code matches). Either alone is insufficient — an internally-consistent spec that the code doesn't implement is still aspirational.

**Triggers.** Trigger spec edit **E11** (one-line banner update).

---

### D11. Promote F22 (`auto` mode never picks `gbdt_quantile`) into spec body

**Background.** Pass 3 F22 codified that the auto-selection logic never returns `gbdt_quantile` — it requires explicit opt-in via classifier `pattern == "spiky"` or operator override. This rule lives only in `v2_revision notes.md`. design_v2.md:543 has *one line* mentioning this; §5 dispatch logic doesn't enumerate it; §7 pattern → forecaster mapping (line 1029) shows `spiky → gbdt_quantile` but doesn't say the auto path bypasses GBDT.

**Options.**
- (a) Add a one-paragraph "auto-selection rules" subsection to §5 (between line 488 "Forecast Service /recommend pipeline" and the per-forecaster sections). Enumerate: *"`auto` returns `prophet` when `len(rps_history) >= PROPHET_MIN_POINTS` and the workload has periodic structure (signaled by classifier pattern); else `linear_extrap`. Never returns `gbdt_quantile` — that path is opt-in only."*
- (b) Leave as-is. Implementers can find the rule by reading the revision notes.

**Recommendation.** (a). Rules that affect classifier-vs-Forecast-Service contract should live in the spec body. Rev-notes are history; they should not be load-bearing on current-state behaviour.

**Triggers.** No code dependency. Trigger spec edit **E19**.

---

### D12. `LINEAR_EXTRAP_TREND_BLEND` semantics contradiction *(spec bug)*

**Background.** design_v2.md:283 lists `LINEAR_EXTRAP_TREND_BLEND` with default `0.7` and prose: *"Blend weight … between the recent-window slope (weight α) and `context.trend_24h_slope` (weight 1-α). Set to `1.0` to disable the blend (recent slope only)"*. Under this reading: `BLEND = α = recent-slope's weight`; `BLEND = 1.0` → recent only.

But Pass 2 F16 in the revision notes pins the formula as:
```
m_blended = (1 - LINEAR_EXTRAP_TREND_BLEND) * m_window + LINEAR_EXTRAP_TREND_BLEND * trend_24h_slope
```
with default `0.30`. Under this reading: `BLEND = trend's weight`; `BLEND = 1.0` → trend only.

Pass 5 F31's intercept-recompute fix is built on F16's formula. The §4 default value (`0.7`) and §4 prose semantics (recent-slope-weight) together compute the *same* steady state as F16's `0.30` default with trend-weight semantics — but the two phrasings have opposite responses to *changing* the parameter, and "set to 1.0 to disable the blend" means opposite things.

**Options.**
- (a) Canonicalise §4's reading. Default stays `0.7`; formula in §5 / F16 / F31 is restated as `m_blended = LINEAR_EXTRAP_TREND_BLEND * m_window + (1 - LINEAR_EXTRAP_TREND_BLEND) * trend_24h_slope`. F31's centroid-recompute is unaffected (it's about `b`, not the polarity of the blend on `m`).
- (b) Canonicalise F16's reading. §4 default flips to `0.30`; §4 prose rewrites to *"weight α on `context.trend_24h_slope`; 1-α on recent slope; set to 0 to disable the blend (recent slope only)"*.
- (c) Rename the env var. Use `LINEAR_EXTRAP_RECENT_WEIGHT` (= α on recent) or `LINEAR_EXTRAP_TREND_WEIGHT` (= α on trend). Forces the polarity to match the name. Then update §4, §5, F16, F31 accordingly.

**Recommendation.** (c) with name `LINEAR_EXTRAP_RECENT_WEIGHT`. Default `0.7`. Formula: `m_blended = LINEAR_EXTRAP_RECENT_WEIGHT * m_window + (1 - LINEAR_EXTRAP_RECENT_WEIGHT) * trend_24h_slope`. *"Set to `1.0` to disable the blend"* reads naturally. F16 / F31 formulae update accordingly. The rename eliminates polarity ambiguity at the name level so future readers don't have to consult prose to disambiguate.

**Triggers.** Unblocked immediately (spec). Code-side trigger: G15 (linear_extrap blend implementation) must use whichever convention wins. Trigger spec edits **E20** (§4 row + prose), **E21** (§5 forecast_linear_extrap step 3), **E22** (revision-notes-history clarification on F16/F31's restatement under the new name — keeps audit trail honest).

---

### D13. Trigger numbering — "three" vs "four"

**Background.** §1 (line 39) says *"Three re-classification triggers: periodic timer, manual annotation, Deployment rollout"*. §6.1 (line 717-724) lists *four* triggers under "Triggers": "1. Immediate first run … 2. Periodic timer … 3. Manual annotation … 4. Deployment rollout". The "immediate first run" is a *first* classification, not a *re*-classification — but §6.1's heading just says "Triggers" without distinction.

**Options.**
- (a) Make §6.1 split *"Initial trigger: 1. Immediate first run"* from *"Re-classification triggers: 1. Periodic timer 2. Manual annotation 3. Deployment rollout"*. §1's "three re-classification triggers" stays correct.
- (b) Make §1 say "four classification triggers" and remove the "re-" prefix. §6.1 stays as-is.
- (c) Pick neither phrasing — rewrite §6.1's intro to *"The worker runs classification on four occasions: …"*. Drop the "trigger" framing.

**Recommendation.** (a). The "first time" vs "subsequent times" distinction is real (first time has no prior `classifiedParams` to compare against; re-classifications can dedupe via `CLASSIFIER_DEDUP_SECONDS`). Splitting them in §6.1 makes the dedup paragraph (line 724) attach to the right scope.

**Triggers.** No code dependency. Trigger spec edit **E23**.

---

## §2. Pending spec edits

These are concrete edits to design_v2.md (or sibling docs). Some are unblocked now; some are gated on a decision in §1; some are gated on a code change in `gap-report-v2.md`.

| ID | Edit | Gate | Notes / dependent files |
|---|---|---|---|
| **E1** | Remove F13 `K_TOD_DOWN` parenthetical disclaimer (design_v2.md:1060) | D1 = (a) **and** code rename lands (G11/G19) | Single line removal once code is renamed. |
| **E2** | Condense §5 step 10 PascalCase ↔ snake_case mapping table (line 484) to a one-liner | D2 = (b) **and** G22 lands | Until then, **E13** keeps the table as-is (just reformatted). |
| **E3** | Tighten F2a-revisited's defensive 1-min-vs-5-min explanatory prose | G11 lands (cadence migration completes) | The paragraph at design_v2.md:259 ("At the default 5-min resolution this is ~6h …") can drop the parenthetical comparison once 1-min is no longer a live alternative in the code. |
| **E4** | Verify §6.1 step 6.5 `trend24hSlope` ↔ §7 `trend_slope` cross-reference fully landed | Unblocked | F25 added partial cross-reference. Audit §4 status example, §5 forecast_linear_extrap step 3, §7 features table, §7 parameter formulae section all cite the same definition. ~30 minutes. |
| **E5** | Add a single source-of-truth cross-reference table mapping §6.2 `ExplainRequest` fields ↔ §4 status fields ↔ §7 features | Unblocked | Currently three separate lists (§6.2:875-897, §4:206-212, §7:972-979). Place the table in §6.2 with back-links from the other two sections. |
| **E6** | Verify §1 ↔ §5 step 10 ↔ §6.2 token enumeration consistency | Unblocked | F40 fixed §1; verify all three sites enumerate the same 16 tokens (incl. the bindings, both `with replica change` and `without replica change` qualifiers). ~20 minutes. |
| **E7** | New sibling doc `docs/migrating-v1-to-v2.md` | D6 = (b) | CRD diff + env-var diff + behaviour-change list + one-line "what to expect" per change. ~half a page. |
| **E8** | New §11 Glossary in design_v2.md | D7 = (a) | ~15 terms × 1 line. Each term links to the §-where-it's-defined. |
| **E9** | New §12 Acceptance criteria in design_v2.md | D8 = (a) | ~20 testable assertions. Each assertion maps to a `gap-report-v2.md` G# (or to existing v1 CI coverage). |
| **E10** | Audit §9 failure-mode table; add rows for any uncovered v2-introduced failure paths | D9 = (a) | Estimated 3–5 new rows: GBDT prediction NaN/negative; `hourly_profile` array length mismatch (more specific than current "malformed context" generic row); `current_hour_utc` / `current_minute_utc` out of range; restart with persisted `rpsPerPodCurrent` out of bounds. |
| **E11** | Update §0 status banner from "Draft for team review" to "Approved" | D10 = (c) **and** §1 + §2 complete **and** gap-report-v2.md G10–G23 closed | One-line edit. The state machine: this is the last edit to land. |
| **E12** | Reaffirm `v2_revision notes.md` as a separate (non-merged) file | Unblocked | Add a one-line note to design_v2.md's header or §0: *"Audit history lives in `v2_revision notes.md` (append-only). This file is current-state only."* Prevents future contributors from "rationalising" by merging history back. |
| **E13** | Reformat §5 step 10 PascalCase ↔ snake_case mapping as a 16-row table (interim) | Unblocked (independent of D2 outcome) | Whether the table eventually condenses (E2) or stays full, format-as-table is strictly better than current paragraph form. Lands now; if E2 fires later it deletes most of the table. |
| **E14** | Reformat §6.2 trigger paragraph (line 834) as a bullet list | Unblocked | Currently one dense paragraph with five distinct rules: trigger criteria, `cooldown_holding_*` exclusion, `max_replicas_binding`-without-replica-change exclusion, ExplainWorker-always-starts, Ollama failure tolerance. ~20 minutes. |
| **E15** | New §7 paragraph on classification-rule boundary stability | D4 = (b) | One paragraph, ~80 words. States the swap is bounded by `CLASSIFIER_INTERVAL_MINUTES` and is operator-visible via `pattern_classified`. No hysteresis added. |
| **E16** | Add `autoscaling.agentic.io/skip-context` row to §4 annotation table | D5 = (b) | One row + ~2 sentences of behaviour. |
| **E17** | Add `skip-context` conditional to §5 step 3 ("omit `context` in `/recommend` request") | D5 = (b) | ~3 lines: *"If `metadata.annotations['autoscaling.agentic.io/skip-context'] == 'true'`, omit the `context` field unconditionally regardless of `status.classifiedParams.context`. The annotation is operator-controlled and persists until removed."* |
| **E18** | Add `skip-context` row to §9 failure-mode table | D5 = (b) | One row pairing the failure mode (*`autoscaling.agentic.io/skip-context: "true"` is set*) with the behaviour (*reconciler omits `context` from `/recommend`; Forecast Service falls back to its context-free path — no safety caps, no hourly regressor; annotation persists until cleared*). |
| **E19** | New §5 subsection on Forecast Service `auto`-mode selection rules | D11 = (a) | One paragraph at the top of the per-forecaster sections. Enumerates the auto rules; explicitly states `gbdt_quantile` is not in the auto path. |
| **E20** | Update §4 `LINEAR_EXTRAP_TREND_BLEND` row | D12 outcome | Per the chosen polarity. If D12 = (c): rename to `LINEAR_EXTRAP_RECENT_WEIGHT`, default `0.7`, prose updated. |
| **E21** | Update §5 `forecast_linear_extrap` step 3 with the canonical formula | D12 outcome | Match the env var name and polarity chosen in D12. |
| **E22** | Add a clarifying note to `v2_revision notes.md` at F16 / F31 | D12 outcome | Preserves audit-trail honesty: *"In current spec the env var is named `<chosen name>` with polarity `<…>`; the formula above is restated accordingly."* No history is rewritten; one note is appended. |
| **E23** | Restructure §6.1 trigger list: split "Initial classification" from "Re-classification triggers" | D13 = (a) | Two short subheadings under "Triggers". `CLASSIFIER_DEDUP_SECONDS` paragraph attaches to the re-classification scope only. ~10 minutes. |

---

## §3. Sequencing

The dependency chains are intentionally short — most edits unlock independently. The few that cluster:

**Unblocked now (ship in any order):**
- E4, E5, E6, E12, E13, E14 — pure spec hygiene; no decisions, no code dependencies.

**Decision-gated (decide in §1, then ship the edit):**
- D4 → E15 *(boundary stability)*
- D5 → E16, E17, E18 *(skip-context annotation)*
- D6 → E7 *(migration guide)*
- D7 → E8 *(glossary)*
- D8 → E9 *(acceptance criteria)*
- D9 → E10 *(§9 expansion)*
- D11 → E19 *(auto-mode subsection)*
- D12 → E20, E21, E22 *(LINEAR_EXTRAP_TREND_BLEND polarity)*
- D13 → E23 *(trigger restructure)*

**Code-gated (waits on `gap-report-v2.md`):**
- D1 + G11/G19 → E1 *(K_TOD_DOWN disclaimer removal)*
- D2 + G22 → E2 *(PascalCase mapping condensation)*
- G11 → E3 *(F2a-revisited prose tightening)*

**Final gate:**
- D10 + all §1/§2 complete + G10–G23 closed → E11 *(banner: Approved)*

Recommended landing order:

1. **First PR — pure hygiene.** E4, E5, E6, E12, E13, E14. No decisions, no code wait. ~half a day; lands immediately.
2. **Second PR — decisions + their unblocked edits.** Decide all of D1–D13 in one pass (D1 and D2's spec *edits* are code-gated, but their *decisions* should still be made now so the trailing edits are pre-cleared). Ship E7, E8, E9, E10, E15, E16, E17, E18, E19, E20, E21, E22, E23. ~one day.
3. **Trailing edits — gated on code.** E1, E2, E3 land alongside the relevant gap-report-v2.md PR sequence (G11/G15/G19/G22). Each is a single-paragraph edit; folded into the code PR.
4. **Banner flip.** E11 lands once gap-report-v2.md is empty and all of the above is done.

Total: ~1.5–2 days of focused spec-revision work, distributed across two-to-three PRs, plus piggy-back edits in the code-side PRs.

---

## §4. Out of scope (with rationale)

- **Operational-defaults recalibration** — `CV_GUARD_MEAN_RPS=1.0`, `GRADUAL_RAMP_DAILY_DRIFT_FRAC=0.20`, `LINEAR_EXTRAP_TREND_BLEND` default value (separate from D12's polarity question), `GBDT_QUANTILE=0.90`, `CLASSIFIER_DEDUP_SECONDS=60`, `HOURLY_PROFILE_MIN_HOURS=12`. Each of these is a numeric default picked by analytical reasoning; revisiting them properly requires data from a v2 nightly run with workloads at the relevant scales. Desk-revisiting them now would be wrong. Track as "tune-later" in a separate ops-tuning doc once v2 has run for a quarter.
- **Renumbering F1–F40 into a new scheme** — the F-numbers are the audit trail. They appear in commit messages, in this artifact, and in `gap-report-v2.md`'s G-descriptions. Renumbering would break every back-reference for cosmetic gain.
- **Folding `v2_revision notes.md` content back into design_v2.md** — Pass 6 explicitly split them. Past-tense audit history and current-state spec serve different audiences and have different mutability profiles. Re-merging would be a regression.
- **Renumbering D# / E# alongside future spec audits** — these are forward-looking IDs in this artifact only. Once the artifact is complete (E11 lands), the IDs are retired; no future audit pass needs to reuse them.
- **Updating gap-report-v2.md to back-reference D# / E# items** — the appendix ledger here is one-way (this doc cites G#). Keeps the two artifacts independently mutable; gap-report-v2.md doesn't churn when this doc adds an item.
- **Cross-version compatibility testing (apiVersion: v1alpha1 → v1beta1)** — no v1beta1 planned; v1alpha1 schema additions are backward-compatible.

---

## §5. Full F1–F40 ledger (appendix)

Cross-tabulation of every Pass 1–6 finding. Verifies completeness — a reviewer can confirm at a glance that every finding has been seen and classified. *"—"* in the spec-revision column means *"spec edit landed in the original pass; no further spec-side action required"*.

| F# | Pass | Spec status | Code gap | Spec-revision item |
|---|---|---|---|---|
| F1 | 1 | spec edit landed (forecast_hw_seasonal removed) | n/a | — |
| F2a | 1 | superseded by F2a-revisited | n/a | — |
| F3a | 1 | spec edit landed (Prophet ds anchor) | G14 | — |
| F4a | 1 | spec edit landed (autocorr lag formula) | G11 | — |
| F5 | 1 | spec edit landed (PROPHET_MIN_POINTS=30) | G21 | — |
| F6 | 1 | spec edit landed (current_hour_utc validation) | G10/G14 | — |
| F7 | 1 | "no change" verdict | n/a | **D3** *(revisit)* |
| F8 | 1 | spec edit landed (illusory test path deleted) | n/a | **D5** *(revisit)* |
| F9 | 1 | "auto-resolved by F1" | n/a | **D4** *(revisit)* |
| F10 | 1 | spec edit landed (per-request vs persisted clarification) | n/a | — |
| F2a-revisited | 2 | spec edit landed (72/240 thresholds) | G11 | **E3** *(prose tightens once G11 lands)* |
| F11 | 2 | spec edit landed (264 example) | n/a | — |
| F12 | 2 | spec edit landed (Pattern!="" trigger) | G18 | — |
| F13 | 2 | spec edit landed w/ disclaimer (`K_PERIODIC_DOWN`) | G11/G19 | **D1 + E1** |
| F14 | 2 | "auto-resolved by F2a-revisited" | n/a | **D3** *(folded with F7)* |
| F15 | 2 | spec edit landed (slope in arch diagram) | n/a | — |
| F16 | 2 | spec edit landed (trend blend formula) | G15 | **D12 + E20 + E21 + E22** *(semantics contradiction)* |
| F17 | 3 | spec edit landed (current_minute_utc) | G14 | — |
| F18 | 3 | spec edit landed (rps/min units) | G11 | — |
| F19 | 3 | spec edit landed (revision annotation watcher) | G16 | — |
| F20 | 3 | spec edit landed (5-copy ring-buffer seed) | G17 | — |
| F21 | 3 | spec edit landed (GBDT timestamp anchor) | G12 | — |
| F22 | 3 | spec edit landed (clarification in revision notes only) | n/a | **D11 + E19** *(promote rationale into spec body)* |
| F23 | 3 | spec edit landed (RPS_PER_POD_NOISE_FLOOR_RPS env var) | G21 | — |
| F24 | 3 | spec edit landed (GBDT_MIN_POINTS env var) | G21 | — |
| F25 | 4 | spec edit landed (partial cross-reference) | n/a | **E4** *(verify all sites cite it)* |
| F26 | 4 | spec edit landed (relative threshold) | G11 | — |
| F27 | 4 | spec edit landed (unboundedRecommended + binding tokens) | G13 | — |
| F28 | 4 | spec edit landed (max(mean,1.0) denominator) | G11 | — |
| F29 | 4 | spec edit landed (CV_GUARD_MEAN_RPS named) | G11 | — |
| F30 | 4 | spec edit landed (priority-rationale paragraph) | n/a | — |
| F31 | 5 | spec edit landed (intercept recompute) | G15 | **D12 + E20 + E21 + E22** *(tied to F16's formula)* |
| F32c | 5 | spec edit landed (env-tunable) | G21 | — |
| F33 | 5 | spec edit landed (prompt conditionals) | G18 | — |
| F34 | 6 | spec edit landed (precedence rule 4) | n/a | — |
| F35 | 6 | spec edit landed (status example arithmetic) | n/a | — |
| F36 | 6 | spec edit landed (env-var placement) | G21 | — |
| F37 | 6 | spec edit landed (webhook strict inequality) | G20 | — |
| F38 | 6 | spec edit landed ("clip" wording) | n/a | — |
| F39 | 6 | spec edit landed (PascalCase mapping table) | G22 | **D2 + E2 + E13** |
| F40 | 6 | spec edit landed (§1 enumeration) | n/a | **E6** *(verify consistency at all 3 sites)* |

**Tally.** 41 finding labels (F1–F40 plus F2a-revisited as a distinct entry). Of these:
- **28 are spec-done with no further spec-side action.** They appear here for completeness; downstream code-side work is tracked in `gap-report-v2.md`.
- **12 have active items in §1 / §2.** Each finding maps to one or more D# / E# IDs (some D# items are conjugate, e.g., D3 covers F7 + F14; D12 covers F16 + F31).
- **0 are missing.**

Plus four net-new items from this audit's spec-side sweep, not derived from any F-finding: **D12** (LINEAR_EXTRAP_TREND_BLEND polarity), **D13** (trigger numbering), **E13** (mapping table reformat), **E14** (§6.2 trigger bullets).
