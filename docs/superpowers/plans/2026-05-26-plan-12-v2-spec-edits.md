# Plan 12 — v2 Spec Edits Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land all unblocked and decision-gated spec edits (E4–E23 except E1/E2/E3/E11) into `docs/design_v2.md` and sibling docs. Ratify D1–D13 by baking their outputs into the spec. Create migration guide, glossary, and acceptance criteria sections.

**Architecture:** Pure documentation work — no code changes. Every task edits markdown files under `docs/`. Verification is grep-based consistency checking. TDD spirit: assert the edit target exists (or does not yet exist), make the edit, verify the assertion flips.

**Tech Stack:** Markdown, grep/rg for verification, git for commits.

---

## Spec Coverage Map

| v2-spec-revision-plan item | Task |
| --- | --- |
| E12 (audit-history note) | T1 |
| E13 (PascalCase table reformat) | T2 |
| E14 (ExplainWorker trigger bullets) | T3 |
| E4 (trend24hSlope cross-ref) | T4 |
| E5 (ExplainRequest cross-ref table) | T5 |
| E6 (token consistency 3-site) | T6 |
| E23 (trigger restructure) | T7 |
| E19 (auto-mode subsection) | T8 |
| E15 (boundary-stability paragraph) | T9 |
| E16 (skip-context annotation row) | T10 |
| E17 (skip-context step 3 conditional) | T11 |
| E18 (skip-context failure-mode row) | T12 |
| E10 (failure-mode table audit) | T13 |
| E20 (LINEAR_EXTRAP_RECENT_WEIGHT row) | T14 |
| E21 (forecast_linear_extrap formula) | T15 |
| E22 (revision notes F16/F31 note) | T16 |
| E7 (migration guide) | T17 |
| E8 (glossary) | T18 |
| E9 (acceptance criteria) | T19 |

---

## File Structure

Files created or modified by this plan:

- Modify: `docs/design_v2.md` (T1–T15, T18, T19)
- Modify: `docs/v2_revision notes.md` (T16)
- Create: `docs/migrating-v1-to-v2.md` (T17)

---

## Sub-PR A: Pure Hygiene (T1–T6)

No decisions needed, no code dependencies. Land immediately.

### Task 1: E12 — Add audit-history note to design_v2.md header

**Files:**
- Modify: `docs/design_v2.md:3` (status line area)

- [ ] **Step 1: Verify note does not exist yet**

Run: `grep -n "Audit history" docs/design_v2.md`
Expected: no output (note not present)

- [ ] **Step 2: Add the note**

Insert after line 3 (the "Date: ... Status: ..." line) a new line:

```markdown
Audit history: `docs/v2_revision notes.md` (append-only). This file is current-state only.
```

- [ ] **Step 3: Verify note is present**

Run: `grep -n "Audit history" docs/design_v2.md`
Expected: matches the new line

- [ ] **Step 4: Commit**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): add audit-history pointer to header (E12)"
```

---

### Task 2: E13 — Reformat PascalCase mapping as a 16-row table

**Files:**
- Modify: `docs/design_v2.md:484` (§5 step 10 PascalCase mapping)

- [ ] **Step 1: Locate the current prose block**

Run: `grep -n "PascalCase" docs/design_v2.md`
Expected: line ~484 with the prose description of the mapping

- [ ] **Step 2: Replace prose with a 16-row table**

Replace the paragraph starting "K8s Event `Reason` field naming:" with a markdown table:

```markdown
K8s Event `Reason` field naming:

| snake_case token | PascalCase `Reason` | Emitter |
| --- | --- | --- |
| `scale_up` | `ScaleUp` | Reconciler |
| `scale_down` | `ScaleDown` | Reconciler |
| `no_change` | `NoChange` | Reconciler |
| `step_capped_up` | `StepCappedUp` | Reconciler |
| `step_capped_down` | `StepCappedDown` | Reconciler |
| `cooldown_holding_up` | `CooldownHoldingUp` | Reconciler |
| `cooldown_holding_down` | `CooldownHoldingDown` | Reconciler |
| `max_replicas_binding` | `MaxReplicasBinding` | Reconciler |
| `min_replicas_binding` | `MinReplicasBinding` | Reconciler |
| `kill_switched` | `KillSwitched` | Reconciler |
| `conflict_detected` | `ConflictDetected` | Reconciler |
| `forecast_unavailable` | `ForecastUnavailable` | Reconciler |
| `metrics_unavailable` | `MetricsUnavailable` | Reconciler |
| `pattern_classified` | `PatternClassified` | ClassifierWorker |
| `pattern_unknown` | `PatternUnknown` | ClassifierWorker |
| `scale_explained` | `ScaleExplained` | ExplainWorker |

The snake_case token is included verbatim in the event message body for log searchability.
```

- [ ] **Step 3: Verify table renders 16 data rows**

Run: `grep -c "| \`" docs/design_v2.md | head -1` (count rows with backtick-quoted tokens in the new table area)
Expected: 16 or more matches in the table section

- [ ] **Step 4: Commit**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): reformat PascalCase mapping as 16-row table (E13)"
```

---

### Task 3: E14 — Reformat ExplainWorker trigger paragraph as bullet list

**Files:**
- Modify: `docs/design_v2.md:834` (§6.2 opening paragraph)

- [ ] **Step 1: Locate the dense paragraph**

Run: `grep -n "Triggered by the reconciler after any event" docs/design_v2.md`
Expected: line ~834

- [ ] **Step 2: Replace paragraph with structured bullet list**

Replace the single paragraph with:

```markdown
**Trigger rules:**

- **Triggering tokens:** `scale_up`, `scale_down`, `step_capped_up`, `step_capped_down`, and (when replicas also changed this reconcile) `max_replicas_binding` / `min_replicas_binding`.
- **Cap-limited scales** still change replica count and warrant explanation.
- **`cooldown_holding_*` exclusion:** These events do not trigger ExplainWorker because step 7 sets `target = current_replicas` and step 8's hysteresis guard skips the `/scale` patch — the trigger criterion (replica change) is not met.
- **Binding without replica change exclusion:** `max_replicas_binding` / `min_replicas_binding` events that fire without a replica change emit the K8s Event for visibility but do not trigger ExplainWorker — prose explanation of "you're at the cap" is low-signal compared to the bare-fact event.
- **ExplainWorker always starts** — no API key gate. If Ollama is unreachable or the model is not pulled, each failed call is logged and the controller continues normally; the next replica-changing event triggers a fresh attempt.
```

- [ ] **Step 3: Verify bullet structure**

Run: `grep -c "^\- \*\*" docs/design_v2.md` in the §6.2 area
Expected: 5 bullet items present

- [ ] **Step 4: Commit**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): restructure ExplainWorker triggers as bullet list (E14)"
```

---

### Task 4: E4 — Verify trend24hSlope cross-reference consistency

**Files:**
- Modify: `docs/design_v2.md` (multiple sections if drift found)

- [ ] **Step 1: Collect all references to trend24hSlope / trend_24h_slope / trend_slope**

Run: `grep -n "trend.*[Ss]lope\|trend_24h" docs/design_v2.md`
Expected: hits in §4 (status fields), §5 (forecast_linear_extrap step 3), §6.1 (step 6.5), §7 (features table + parameter formulae)

- [ ] **Step 2: Verify unit consistency**

All references must state or imply "rps/min". Check each hit:
- §4 status field (`trend24hSlope`): should say "rps/min"
- §5 forecast_linear_extrap step 3: should reference "Both are in rps/min"
- §6.1 step 6.5: should show the division by `CONTEXT_DOWNSAMPLE_RESOLUTION_MIN`
- §7 features table (`trend_slope`): should say "Already in rps/min"

- [ ] **Step 3: Fix any drift found**

If any site lacks the "rps/min" label or the cross-reference note from F25, add it. The canonical phrasing: "Identical to `context.trend24hSlope` (see section 6.1 step 6.5) -- already in rps/min."

- [ ] **Step 4: Commit (if changes made)**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): verify trend24hSlope cross-refs at all sites (E4)"
```

---

### Task 5: E5 — Add ExplainRequest cross-reference table

**Files:**
- Modify: `docs/design_v2.md` (insert after §6.2 ExplainRequest fields table, ~line 897)

- [ ] **Step 1: Verify table does not exist yet**

Run: `grep -n "Cross-reference.*ExplainRequest\|Field provenance" docs/design_v2.md`
Expected: no output

- [ ] **Step 2: Insert cross-reference table**

After the ExplainRequest fields table in §6.2, add:

```markdown
#### Field provenance (cross-reference)

| ExplainRequest field | Source in §4 status | Source in §7 features |
| --- | --- | --- |
| `ReasoningToken` | — | — |
| `CurrentRPS` | — (Prometheus query) | — |
| `PredictedRPS` | — (Forecast Service) | — |
| `CurrentReplicas` | — (Deployment status) | — |
| `RecommendedReplicas` | `status.recommendedReplicas` | — |
| `UnboundedRecommended` | `status.unboundedRecommended` | — |
| `TargetReplicas` | — (post-cap local) | — |
| `MaxReplicas` | `spec.maxReplicas` | — |
| `MinReplicas` | `spec.minReplicas` | — |
| `HorizonMinutes` | — (Forecast Service response) | — |
| `ModelUsed` | — (Forecast Service response) | — |
| `Pattern` | `status.classifiedParams.pattern` | §7 classification output |
| `Confidence` | `status.classifiedParams.confidence` | — |
| `BaselineRPS` | `status.classifiedParams.context.baselineRPS` | §6.1 step 6.5 |
| `PeakP95RPS` | `status.classifiedParams.context.peakP95RPS` | §6.1 step 6.5 |
| `HourlyProfileValid` | `status.classifiedParams.context.hourlyProfileValid` | §6.1 step 6.5 |
| `EffectiveCooldownUp` | — (reconcile preamble) | — |
| `EffectiveCooldownDown` | — (reconcile preamble) | — |
| `EffectiveMaxStep` | — (reconcile preamble) | — |
```

- [ ] **Step 3: Add back-link notes in §4 and §7**

In §4 after the `status.classifiedParams.context` field list, add: "_(See §6.2 "Field provenance" table for how these flow to ExplainWorker.)_"

In §7 features table header, add: "_(These features also appear in ExplainRequest — see §6.2 "Field provenance".)_"

- [ ] **Step 4: Verify table renders**

Run: `grep -n "Field provenance" docs/design_v2.md`
Expected: one hit in §6.2

- [ ] **Step 5: Commit**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): add ExplainRequest field provenance cross-ref table (E5)"
```

---

### Task 6: E6 — Verify token enumeration consistency across 3 sites

**Files:**
- Modify: `docs/design_v2.md` (only if inconsistency found)

- [ ] **Step 1: Extract token lists from all three sites**

Site 1 — §1 (line ~15): ExplainWorker trigger tokens.
Site 2 — §5 step 10 (lines ~476–484): all reasoning tokens + PascalCase table.
Site 3 — §6.2 (line ~834): ExplainWorker trigger tokens.

Run: `grep -n "scale_up\|scale_down\|step_capped\|cooldown_holding\|max_replicas_binding\|min_replicas_binding\|kill_switched\|conflict_detected\|forecast_unavailable\|metrics_unavailable" docs/design_v2.md`

- [ ] **Step 2: Verify §5 step 10 table has exactly 16 tokens**

Count: scale_up, scale_down, no_change, step_capped_up, step_capped_down, cooldown_holding_up, cooldown_holding_down, max_replicas_binding, min_replicas_binding, kill_switched, conflict_detected, forecast_unavailable, metrics_unavailable, pattern_classified, pattern_unknown, scale_explained = 16.

- [ ] **Step 3: Verify §1 and §6.2 trigger lists match**

Both should list exactly: `scale_up`, `scale_down`, `step_capped_up`, `step_capped_down`, `max_replicas_binding` / `min_replicas_binding` (with the "when replicas changed" qualifier for the last two).

- [ ] **Step 4: Fix any inconsistency**

If any site is missing a token or has extra text that contradicts another site, align to the §5 step 10 table (authoritative).

- [ ] **Step 5: Commit (if changes made)**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): verify token enumeration consistency at all 3 sites (E6)"
```

---

## Sub-PR B: Decision-Gated Edits (T7–T19)

Each task here implements the output of a ratified decision from the strategy doc §2.

### Task 7: E23 — Restructure trigger list (D13)

**Files:**
- Modify: `docs/design_v2.md:719-724` (§6.1 Triggers section)

- [ ] **Step 1: Locate the four-item trigger list**

Run: `grep -n "Immediate first run\|Periodic timer\|Manual annotation\|Deployment rollout" docs/design_v2.md`
Expected: four hits in §6.1, numbered 1–4

- [ ] **Step 2: Split into two subheadings**

Replace the current "#### Triggers" section with:

```markdown
#### Initial classification trigger

1. **Immediate first run** — when the reconciler first sees a CR, the worker runs classification once before starting its periodic timer. If fewer than `CLASSIFIER_MIN_POINTS` history points exist in Prometheus, it emits `pattern_unknown` and waits for the next trigger; otherwise the CR reaches its classified state without waiting up to `CLASSIFIER_INTERVAL_MINUTES` for the first timer tick.

#### Re-classification triggers

1. **Periodic timer** — fires every `CLASSIFIER_INTERVAL_MINUTES` minutes (default 30) after the immediate first run.
2. **Manual annotation** — operator sets `autoscaling.agentic.io/reclassify: "true"` on the CR. A controller-runtime watcher on `AgenticAutoscaler` observes the annotation change and signals the worker to run classification immediately; the worker then removes the annotation via a patch. Intended for use after a known traffic-pattern change (e.g., a major deploy or a product launch).
3. **Deployment rollout** — a controller-runtime watcher (informer) on the target Deployment observes changes to the `deployment.kubernetes.io/revision` annotation and signals the worker to re-classify immediately, since new code often changes traffic characteristics. The revision annotation is incremented by the Deployment controller only on actual rollouts (image / env / command changes that produce a new ReplicaSet) — **not** on `/scale` patches. We deliberately do **not** watch `metadata.generation`, because the apiserver bumps that field on every `spec.replicas` update too, which would cause this trigger to fire on every reconcile that scales — defeating the purpose.

**Dedup:** The worker skips any re-classification trigger if any prior classification ran within the last `CLASSIFIER_DEDUP_SECONDS` seconds (so the initial-sync race between informer's first emit and the immediate first run collapses to a single classification cycle).
```

- [ ] **Step 3: Verify §1 still says "Three re-classification triggers"**

Run: `grep -n "Three re-classification" docs/design_v2.md`
Expected: matches; §1 is now consistent with §6.1's "Re-classification triggers" subheading listing exactly 3.

- [ ] **Step 4: Commit**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): split initial vs re-classification triggers in section 6.1 (E23, D13)"
```

---

### Task 8: E19 — Add auto-mode selection rules subsection (D11)

**Files:**
- Modify: `docs/design_v2.md` (insert before the per-forecaster sections, after line ~544)

- [ ] **Step 1: Locate insertion point**

Run: `grep -n "forecast_linear_extrap\|forecast_prophet\|forecast_gbdt" docs/design_v2.md | head -5`
Expected: first per-forecaster heading around line 546–548

- [ ] **Step 2: Insert auto-mode rules paragraph**

Before the `### forecast_linear_extrap` heading, add:

```markdown
### Auto-mode selection rules

When `preferred_model` is absent, null, or `"auto"`, the Forecast Service selects a model as follows:

1. If `len(rps_history) >= PROPHET_MIN_POINTS` (default 30): use `forecast_prophet`. On Prophet failure, fall back to `forecast_linear_extrap`.
2. Otherwise: use `forecast_linear_extrap`.

**`auto` mode never returns `gbdt_quantile`.** The GBDT path is intentionally opt-in — it runs only when:
- The classifier writes `preferredForecaster: "gbdt_quantile"` (driven by `pattern == "spiky"`; see §7), OR
- An operator explicitly sets `spec.preferredForecaster: "gbdt_quantile"` on the CR.

This keeps the cold-path classifier in charge of when "spiky workload, predict-the-burst" semantics apply, rather than letting the Forecast Service infer it from `rps_history` alone.
```

- [ ] **Step 3: Verify the subsection exists**

Run: `grep -n "Auto-mode selection rules" docs/design_v2.md`
Expected: one hit

- [ ] **Step 4: Commit**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): add auto-mode selection rules subsection (E19, D11)"
```

---

### Task 9: E15 — Add boundary-stability paragraph to §7 (D4)

**Files:**
- Modify: `docs/design_v2.md` (insert after classification rules table, before "Why `periodic` outranks `spiky`")

- [ ] **Step 1: Locate insertion point**

Run: `grep -n "Why.*periodic.*outranks.*spiky" docs/design_v2.md`
Expected: line ~993

- [ ] **Step 2: Insert boundary-stability paragraph**

Before the "Why `periodic` outranks `spiky`" paragraph, add:

```markdown
**Boundary stability.** When a feature value crosses a classification threshold (e.g., `hourly_autocorr` drops from 0.72 to 0.68, crossing the 0.70 boundary), the next classification cycle picks a new pattern and consequently a new preferred forecaster. The hot path then routes new `/recommend` requests to the new forecaster starting from the next reconcile. This swap is bounded by the classifier cadence (`CLASSIFIER_INTERVAL_MINUTES`, default 30 min) — it cannot happen faster than once per cold-path tick. The swap is operator-visible via the `pattern_classified` event (which reports both the new pattern and the new effective forecaster). No classification-rule hysteresis is applied; the thresholds are hard boundaries.
```

- [ ] **Step 3: Verify**

Run: `grep -n "Boundary stability" docs/design_v2.md`
Expected: one hit in §7

- [ ] **Step 4: Commit**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): add boundary-stability paragraph to section 7 (E15, D4)"
```

---

### Task 10: E16 — Add skip-context annotation row to §4 (D5)

**Files:**
- Modify: `docs/design_v2.md` (§4 annotations table, after line ~200)

- [ ] **Step 1: Locate annotations table**

Run: `grep -n "autoscaling.agentic.io/reclassify" docs/design_v2.md`
Expected: line ~200 (second annotation row)

- [ ] **Step 2: Add new row after the reclassify row**

Insert a new table row:

```markdown
| `autoscaling.agentic.io/skip-context` | `"true"` | When set, the reconciler omits the `context` field from `/recommend` requests unconditionally, regardless of `status.classifiedParams.context`. The Forecast Service falls back to its context-free path (no safety caps, no hourly regressor). The annotation persists until the operator removes it. Intended for ad-hoc testing of context-free behaviour without clearing classifier state. |
```

- [ ] **Step 3: Verify**

Run: `grep -n "skip-context" docs/design_v2.md`
Expected: one hit in §4

- [ ] **Step 4: Commit**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): add skip-context annotation to section 4 (E16, D5)"
```

---

### Task 11: E17 — Add skip-context conditional to §5 step 3

**Files:**
- Modify: `docs/design_v2.md` (§5 reconcile loop, near the effectiveContext resolution)

- [ ] **Step 1: Locate effectiveContext line**

Run: `grep -n "effectiveContext" docs/design_v2.md`
Expected: hits around line 309 (preamble) and line 314 (note)

- [ ] **Step 2: Update the effectiveContext resolution**

After the existing `effectiveContext = status.classifiedParams.context` line in the preamble code block, add:

```
if metadata.annotations["autoscaling.agentic.io/skip-context"] == "true":
    effectiveContext = nil   // operator override; context omitted from /recommend
```

- [ ] **Step 3: Update the "Note on effectiveContext" paragraph**

Append to the existing note: "Exception: if the `autoscaling.agentic.io/skip-context` annotation is set to `\"true\"`, the reconciler treats context as nil regardless of classifier state (see §4 annotations table)."

- [ ] **Step 4: Verify**

Run: `grep -n "skip-context" docs/design_v2.md`
Expected: hits in §4 (from T10) and §5 (this task)

- [ ] **Step 5: Commit**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): add skip-context conditional to section 5 preamble (E17, D5)"
```

---

### Task 12: E18 — Add skip-context row to §9 failure-mode table

**Files:**
- Modify: `docs/design_v2.md:1077-1100` (§9 failure table)

- [ ] **Step 1: Locate §9 table**

Run: `grep -n "Failure.*Behavior\|Failure.*behavior" docs/design_v2.md`
Expected: line ~1079

- [ ] **Step 2: Add new row**

Insert before the last row ("No retries within a single reconcile..."):

```markdown
| `autoscaling.agentic.io/skip-context` annotation is set to `"true"` | Reconciler omits `context` from `/recommend`; Forecast Service falls back to context-free forecasting (no safety caps, no hourly regressor). Annotation persists until cleared by operator. |
```

- [ ] **Step 3: Verify**

Run: `grep -n "skip-context" docs/design_v2.md`
Expected: hits in §4, §5, and §9

- [ ] **Step 4: Commit**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): add skip-context failure-mode row to section 9 (E18, D5)"
```

---

### Task 13: E10 — Audit §9 and add new failure-mode rows (D9)

**Files:**
- Modify: `docs/design_v2.md:1077-1100` (§9 failure table)

- [ ] **Step 1: Count current rows**

Run: `grep -c "^|" docs/design_v2.md` in the §9 table area (lines 1079–1100)
Expected: ~22 rows (header + 20 data rows + skip-context from T12)

- [ ] **Step 2: Add 4 new rows for uncovered v2 failure paths**

Insert before the closing "No retries..." line:

```markdown
| `forecast_gbdt_quantile` returns NaN or negative prediction | Treat as a model failure; fall back to `forecast_linear_extrap`. Increment `forecast_gbdt_quantile_failures_total`. Log the invalid output for debugging. |
| `context.hourly_profile` array length is not exactly 24 | Malformed context; drop context (treat as absent), log warning. Proceed with context-free forecasting. |
| `context.current_hour_utc` outside [0, 23] or `context.current_minute_utc` outside [0, 59] | Malformed context; drop context (treat as absent), log warning. Proceed with context-free forecasting. |
| Controller restarts with persisted `status.rpsPerPodCurrent` that is outside `[rpsPerPodMin, rpsPerPodMax]` | Seed the ring buffer with the persisted value regardless (it represents observed reality, not operator bounds). The bounds are only enforced on the `rpsPerPod` field exposed to the scaling formula, not on what the ring buffer stores. Over subsequent reconciles, fresh observations will converge the median. |
```

- [ ] **Step 3: Verify row count increased**

Run: count §9 table rows
Expected: 4 more than before (total ~26 data rows)

- [ ] **Step 4: Commit**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): add 4 failure-mode rows for v2-introduced paths (E10, D9)"
```

---

### Task 14: E20 — Rename LINEAR_EXTRAP_TREND_BLEND to LINEAR_EXTRAP_RECENT_WEIGHT in §4 (D12)

**Files:**
- Modify: `docs/design_v2.md:283` (§4 Forecast Service env-var table)

- [ ] **Step 1: Locate the current row**

Run: `grep -n "LINEAR_EXTRAP_TREND_BLEND" docs/design_v2.md`
Expected: line ~283 in the env-var table

- [ ] **Step 2: Replace the row**

Replace the `LINEAR_EXTRAP_TREND_BLEND` row with:

```markdown
| `LINEAR_EXTRAP_RECENT_WEIGHT` | `0.7` | Blend weight on the recent-window slope in `forecast_linear_extrap`. Formula: `m_blended = LINEAR_EXTRAP_RECENT_WEIGHT * m_window + (1 - LINEAR_EXTRAP_RECENT_WEIGHT) * context.trend_24h_slope`. Set to `1.0` to disable the blend (recent slope only); set lower to lean more on the long-horizon trend. Only applies when `context.trend_24h_slope` is present. |
```

- [ ] **Step 3: Verify old name is gone**

Run: `grep -n "LINEAR_EXTRAP_TREND_BLEND" docs/design_v2.md`
Expected: zero hits (may still appear in revision-notes cross-references — that is handled in T16)

- [ ] **Step 4: Commit**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): rename LINEAR_EXTRAP_TREND_BLEND to LINEAR_EXTRAP_RECENT_WEIGHT in section 4 (E20, D12)"
```

---

### Task 15: E21 — Update forecast_linear_extrap step 3 formula (D12)

**Files:**
- Modify: `docs/design_v2.md:551-568` (§5 forecast_linear_extrap step 3)

- [ ] **Step 1: Locate step 3 code block**

Run: `grep -n "LINEAR_EXTRAP_TREND_BLEND\|LINEAR_EXTRAP_RECENT_WEIGHT" docs/design_v2.md`
Expected: After T14, only the §5 code block should still reference the old name (if not yet updated)

- [ ] **Step 2: Update the formula and comments in step 3**

Replace references in the step-3 code block:
- `LINEAR_EXTRAP_TREND_BLEND * m` becomes `LINEAR_EXTRAP_RECENT_WEIGHT * m`
- `(1 - LINEAR_EXTRAP_TREND_BLEND) * context.trend_24h_slope` stays the same shape but with the new name
- Update the comment: "Default LINEAR_EXTRAP_RECENT_WEIGHT = 0.7 -- favor the recent window..."
- The rationale paragraph below the code block: update any mention of the old name

- [ ] **Step 3: Update the §5 rationale paragraph**

The "Rationale for the trend blend" paragraph (~line 579) should reference the new name.

- [ ] **Step 4: Verify no remaining old-name references**

Run: `grep -n "LINEAR_EXTRAP_TREND_BLEND" docs/design_v2.md`
Expected: zero hits in design_v2.md (revision notes will still have the historical name — that's correct)

- [ ] **Step 5: Commit**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): update forecast_linear_extrap formula to use LINEAR_EXTRAP_RECENT_WEIGHT (E21, D12)"
```

---

### Task 16: E22 — Add clarifying note to v2_revision notes at F16/F31 (D12)

**Files:**
- Modify: `docs/v2_revision notes.md` (append note near F16 and F31 entries)

- [ ] **Step 1: Locate F16 and F31 references**

Run: `grep -n "F16\|F31" "docs/v2_revision notes.md"`
Expected: hits in the findings table

- [ ] **Step 2: Append clarifying note**

At the end of the file (or after the relevant finding entries), add:

```markdown
**D12 resolution note (2026-05-26):** The env var formerly named `LINEAR_EXTRAP_TREND_BLEND` (referenced in F16 and F31) has been renamed to `LINEAR_EXTRAP_RECENT_WEIGHT` in design_v2.md with polarity: `m_blended = LINEAR_EXTRAP_RECENT_WEIGHT * m_window + (1 - LINEAR_EXTRAP_RECENT_WEIGHT) * trend_24h_slope`. Default `0.7` (favors recent slope). The formula above is restated under the new name; no historical findings are rewritten.
```

- [ ] **Step 3: Verify**

Run: `grep -n "D12 resolution" "docs/v2_revision notes.md"`
Expected: one hit

- [ ] **Step 4: Commit**

```bash
git add "docs/v2_revision notes.md"
git commit -m "docs(revision-notes): add D12 resolution note for F16/F31 LINEAR_EXTRAP_RECENT_WEIGHT rename (E22)"
```

---

### Task 17: E7 — Create migration guide (D6)

**Files:**
- Create: `docs/migrating-v1-to-v2.md`

- [ ] **Step 1: Verify file does not exist**

Run: `ls docs/migrating-v1-to-v2.md`
Expected: "No such file"

- [ ] **Step 2: Create the migration guide**

Create `docs/migrating-v1-to-v2.md` with the following content:

```markdown
# Migrating from v1 to v2

**Date:** 2026-05-26
**Applies to:** Operators upgrading from design.md (v1) to design_v2.md (v2).

---

## CRD changes (additive)

| Change | Impact |
| --- | --- |
| `preferredForecaster` enum adds `gbdt_quantile` | Existing CRs with `prophet`, `linear_extrap`, or `auto` are unaffected. New option available. |
| `status.classifiedParams.context` sub-object added | Read-only status field; no operator action needed. |
| `status.unboundedRecommended` field added | Read-only status field; surfaces capacity-planning signal. |

## Env-var changes

| Env var | v1 default | v2 default | Notes |
| --- | --- | --- | --- |
| `CLASSIFIER_MIN_POINTS` | 70 | 72 | Raised to satisfy `L + 10` at 5-min resolution. |
| `PROPHET_MIN_POINTS` | 60 | 30 | Lowered to let Prophet engage earlier. |
| `FORECAST_HORIZON_MINUTES` | Controller env | Forecast Service env only | Controller no longer needs this; reads from response. |
| `LINEAR_EXTRAP_RECENT_WEIGHT` | (new) | 0.7 | Replaces non-existent `LINEAR_EXTRAP_TREND_BLEND`. |
| `CONTEXT_DOWNSAMPLE_RESOLUTION_MIN` | (new) | 5 | Cold-path downsampling resolution. |
| `CV_GUARD_MEAN_RPS` | (new) | 1.0 | CV zero-guard threshold. |
| `RPS_PER_POD_NOISE_FLOOR_RPS` | hardcoded 10 | env, default 10 | Now operator-tunable. |
| `GBDT_QUANTILE` | (new) | 0.90 | GBDT upper quantile. |
| `GBDT_MIN_POINTS` | (new) | 30 | GBDT minimum history. |
| `PROPHET_USE_HOURLY_REGRESSOR` | (new) | true | Enables hourly regressor in Prophet. |

## Behaviour changes

| What changed | What to expect |
| --- | --- |
| Cold path runs at 5-min resolution (was 1-min) | Classifier uses fewer, smoother data points. `CLASSIFIER_MIN_POINTS=72` at 5-min = ~6h of history. |
| Classifier writes `context` block | Hot path forwards context to Forecast Service; forecasters now anchor on long-horizon data. |
| New reasoning tokens: `max_replicas_binding`, `min_replicas_binding` | Events surface when CRD bounds are binding. `unboundedRecommended` shows what forecast wanted. |
| K8s Event `Reason` field uses PascalCase | `kubectl get events` shows `ScaleUp` instead of `scale_up`. snake_case remains in message body. |
| Ring buffer seeds 5 copies on restart | Post-restart `rpsPerPod` is more stable; fewer spurious scale decisions in first 5 reconciles. |
| Generation watcher replaced by revision annotation | Re-classification no longer fires on every scale event. Only real rollouts trigger it. |

## What to do

1. **Before upgrading:** Note any custom `CLASSIFIER_MIN_POINTS` or `PROPHET_MIN_POINTS` overrides. v2 defaults may be different from what you're running.
2. **During upgrade:** Apply new CRD manifests (additive; no breaking changes). Deploy new controller + Forecast Service images.
3. **After upgrade:** Watch for `pattern_classified` events confirming the classifier ran with the new thresholds. Check that `status.classifiedParams.context` is populated after the first classification cycle.
```

- [ ] **Step 3: Verify file created**

Run: `test -f docs/migrating-v1-to-v2.md && echo OK`
Expected: OK

- [ ] **Step 4: Commit**

```bash
git add docs/migrating-v1-to-v2.md
git commit -m "docs: create v1-to-v2 migration guide (E7, D6)"
```

---

### Task 18: E8 — Add Glossary section to design_v2.md (D7)

**Files:**
- Modify: `docs/design_v2.md` (append new §11 after §9 / before EOF)

- [ ] **Step 1: Verify glossary does not exist**

Run: `grep -n "Glossary" docs/design_v2.md`
Expected: no output

- [ ] **Step 2: Add §11 Glossary**

Append after the last line of §9 (before EOF):

```markdown

## **10. Glossary**

| Term | Definition | Defined in |
| --- | --- | --- |
| `baseline_rps` | Median RPS over the full classifier window | §6.1 step 6.5 |
| `peak_p95_rps` | 95th-percentile RPS over the full classifier window | §6.1 step 6.5 |
| `hourly_autocorr` | Pearson correlation of the series with its lag-L shift (L = 60/resolution) | §7 Features |
| `trend_24h_slope` / `trend_slope` | Least-squares slope in rps/min over the full window | §6.1 step 6.5, §7 Features |
| `hourly_profile` | Length-24 array of median RPS per UTC hour | §6.1 step 6.5, §4 status |
| `hourly_profile_valid` | True if >= HOURLY_PROFILE_MIN_HOURS distinct hours observed | §6.1 step 6.5, §4 status |
| `peak_to_trough` | `percentile_99(series) / max(mean(series), 1.0)` | §7 Features |
| `cv` | `stddev / mean`; zero-guarded below CV_GUARD_MEAN_RPS | §7 Features |
| `rps_per_pod` | Sliding-window median of `current_rps / current_replicas` | §5 step 5 |
| `unboundedRecommended` | Raw forecaster-derived replica count before CRD-bound clamping | §5 step 5, §4 status |
| `recommendedReplicas` | Post-CRD-bounds, pre-cap, pre-cooldown recommendation | §5 step 11, §4 status |
| `effectiveContext` | Context object forwarded to /recommend (nil on cold start) | §5 preamble |
| `effectiveForecaster` | Resolved forecaster after precedence chain | §5 preamble |
| `current_hour_utc` | Controller's UTC hour at request time; forwarded in context | §5 /recommend input |
| `current_minute_utc` | Controller's UTC minute at request time; forwarded in context | §5 /recommend input |
```

- [ ] **Step 3: Verify**

Run: `grep -n "Glossary" docs/design_v2.md`
Expected: one hit (the new section heading)

- [ ] **Step 4: Commit**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): add Glossary section (E8, D7)"
```

---

### Task 19: E9 — Add Acceptance Criteria section to design_v2.md (D8)

**Files:**
- Modify: `docs/design_v2.md` (append new §12 after Glossary)

- [ ] **Step 1: Verify section does not exist**

Run: `grep -n "Acceptance criteria\|Acceptance Criteria" docs/design_v2.md`
Expected: no output

- [ ] **Step 2: Add §12 Acceptance Criteria**

Append after the Glossary section:

```markdown

## **11. Acceptance criteria**

Each assertion below must be testable in CI (unit, integration, or nightly E2E). The assertion's gap-report reference (G#) indicates which code gap delivers it; assertions without a G# are already covered by v1 CI.

1. CRD `preferredForecaster` enum accepts `prophet`, `linear_extrap`, `gbdt_quantile`, `auto`. (G20)
2. Webhook rejects `maxReplicas <= minReplicas`. (G20)
3. `status.classifiedParams.context` is populated with all 5 fields after first successful classification. (G10, G11)
4. Controller forwards `context` object in every `/recommend` call when context is present. (G10)
5. Controller omits `context` when `status.classifiedParams.context` is nil (cold start). (G10)
6. Controller omits `context` when `autoscaling.agentic.io/skip-context: "true"` is set. (Phase 1 spec)
7. Forecast Service `/recommend` accepts `context` field and validates sub-fields. (G10)
8. For a `spiky` workload, auto-mode `/recommend` returns `model_used != "gbdt_quantile"`. (G12, G19)
9. For a `spiky` workload with `preferredForecaster: "gbdt_quantile"`, `/recommend` returns `model_used == "gbdt_quantile"`. (G12, G19)
10. Prophet's `ds[-1]` aligns to `(context.current_hour_utc, context.current_minute_utc)` regardless of Forecast Service pod clock. (G14)
11. Prophet adds `hour_baseline` regressor when `hourly_profile_valid` is true and `PROPHET_USE_HOURLY_REGRESSOR=true`. (G14)
12. `forecast_linear_extrap` blends slope using `LINEAR_EXTRAP_RECENT_WEIGHT` and recomputes intercept at centroid. (G15)
13. `forecast_linear_extrap` clips prediction at `peak_p95_rps * 1.5` when context is present. (G15)
14. When forecast exceeds `maxReplicas`, event message contains `unboundedRecommended` and token is `max_replicas_binding`. (G13)
15. ExplainWorker prompt includes "Long-term context" line when `Pattern != ""`. (G18)
16. ExplainWorker prompt includes binding-constraint explanation when token is `max_replicas_binding` or `min_replicas_binding`. (G18)
17. `/scale` patch does NOT trigger re-classification (revision annotation unchanged). (G16)
18. Ring buffer `Seed()` writes 5 copies of the persisted value. (G17)
19. K8s Event `Reason` field uses PascalCase; message body contains snake_case token. (G22)
20. Classifier uses 5-min resolution Prometheus query and `L = 60 / CONTEXT_DOWNSAMPLE_RESOLUTION_MIN`. (G11)
21. Classifier `gradual_ramp` rule uses relative threshold `abs(slope) * 1440 / max(mean, 1) > 0.20`. (G11)
22. Classifier `peak_to_trough` uses `p99 / max(mean, 1.0)` denominator. (G11)
23. Env var `CLASSIFIER_MIN_POINTS` default is 72; validation floor derives from `L + 10`. (G21)
```

- [ ] **Step 3: Verify**

Run: `grep -n "Acceptance criteria" docs/design_v2.md`
Expected: one hit (the new section heading)

- [ ] **Step 4: Commit**

```bash
git add docs/design_v2.md
git commit -m "docs(design_v2): add Acceptance Criteria section (E9, D8)"
```

---

## Self-Review

After completing all 19 tasks:

- [ ] **Spec coverage:** Every E4–E23 item (except E1/E2/E3/E11 which are code-gated or final-gate) has a task above.
- [ ] **Placeholder scan:** No "TBD", "TODO", or "implement later" in any task.
- [ ] **Name consistency:** `LINEAR_EXTRAP_RECENT_WEIGHT` used consistently after T14 (never the old name in new text).
- [ ] **Section numbering:** After T18 adds §10 Glossary and T19 adds §11 Acceptance Criteria, verify the existing §10-forward sections (if any) are renumbered. Currently design_v2.md has §1–§9; the new sections are §10 and §11 (no collision).
- [ ] **Cross-references:** T5's provenance table references match T18's glossary terms.
- [ ] **Token counts:** T2's 16-row table and T6's verification both agree on 16 tokens.
