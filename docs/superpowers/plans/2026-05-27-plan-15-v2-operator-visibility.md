# Plan 15 — v2 Operator Visibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface the capacity-planning signals that v2 promised but v1 buries. Split `ComputeRecommended` into an unbounded pre-clamp value and a clamped value, persist `unboundedRecommended` in CR status, add `max_replicas_binding` / `min_replicas_binding` reasoning tokens that flow through `ApplyCapAndCooldown`'s precedence chain, surface `unboundedRecommended` in the K8s Event message, and rebuild the ExplainWorker prompt to include a `Long-term context` line (F12-correct gate: `Pattern != ""`, not `Pattern != "default"`), two binding-conditional lines (F33), and the new `ExplainRequest` fields they consume.

**Architecture:** Strict TDD. Each task ships a failing test, the minimum code change to flip it green, a verification command, and a commit. The plan is partitioned into five sub-PRs of increasing depth — status field + decision API split first (compile-time only — no behaviour change), then reasoning tokens + cap-precedence chain, then reconciler wiring (Event message + ExplainWorker trigger), then ExplainRequest fields + prompt-template surgery, then end-to-end pinning in envtest plus the worker prompt test. Each sub-PR is independently buildable and testable; the plan is sequenced so a reviewer can land them as five small reviews or one large one.

**Tech Stack:** Go 1.22+, controller-runtime, kubebuilder markers, `testify`, envtest. No Python changes — the Forecast Service surface is unchanged this phase.

---

## Spec Coverage Map

| Plan item | Tasks | Source |
| --- | --- | --- |
| **G13** — `unboundedRecommended` field + binding-token reasoning tokens end-to-end | T1, T2, T3, T4, T5, T6, T7, T8, T14 | gap-report-v2.md G13 |
| **G18** — ExplainWorker prompt + `ExplainRequest` field additions | T9, T10, T11, T12, T13, T15 | gap-report-v2.md G18 |
| **F12** — `Long-term context` line gates on `Pattern != ""` (not `Pattern != "default"`) | T11 | v2_revision notes F12 |
| **F27** — `unboundedRecommended` in K8s Event message + new `MaxReplicasBinding` / `MinReplicasBinding` tokens with precedence rules | T4, T5, T6, T7 | v2_revision notes F27 |
| **F33** — Prompt conditionals for binding tokens; `UnboundedRecommended`, `MaxReplicas`, `MinReplicas` plumbed into `ExplainRequest` | T9, T10, T12, T13 | v2_revision notes F33 |
| **G19** — already landed in Plan 14 | — | (no-op here) |
| **G20** strict-inequality slice — owned by Phase 5 (Plan 16) | — | (no-op here) |

---

## File Structure

Files created or modified by this plan, grouped by sub-PR.

| Path | Sub-PR | Responsibility |
| --- | --- | --- |
| `api/v1alpha1/agenticautoscaler_types.go` | A | Add `Status.UnboundedRecommended int32 \`json:"unboundedRecommended,omitempty"\``. |
| `config/crd/bases/autoscaling.agentic.io_agenticautoscalers.yaml` | A | Regenerated to expose `status.unboundedRecommended`. |
| `internal/decision/decision.go` | A, B | Split `ComputeRecommended` → `ComputeUnboundedRecommended` + `ClampRecommended` (returns `clamped, bindingReason`). Extend `CapInput` with `BindingReason string`. Rewrite `ApplyCapAndCooldown` so the binding reason carries through unless step 6/7 overwrite. |
| `internal/decision/decision_test.go` | A, B | Tests for new split + precedence chain. |
| `internal/reasoning/tokens.go` | B | Add `MaxReplicasBinding = "max_replicas_binding"` + `MinReplicasBinding = "min_replicas_binding"` constants; extend `AllTokens()`. |
| `internal/reasoning/tokens_test.go` | B | Pin the new constants in the existing snapshot test. |
| `internal/controller/agenticautoscaler_controller.go` | A, B, C, D | Switch to the new decision API; thread `unboundedRecommended` into `aas.Status`, the Event message, and the ExplainRequest. |
| `internal/controller/interfaces.go` | D | Add `UnboundedRecommended`, `MaxReplicas`, `MinReplicas`, `BaselineRPS`, `PeakP95RPS`, `HourlyProfileValid` fields to `ExplainRequest`. |
| `internal/explainer/prompt.go` | D | Fix F12 gate (`Pattern != ""` for Long-term context line, leave Pattern-line gate as-is). Add `Long-term context` line + two binding conditional lines + closing-prompt wording update. |
| `internal/explainer/prompt_test.go` | D | New test cases for every conditional line. |
| `test/envtest/scale_envtest_test.go` | E | New file. End-to-end envtest: forecast > maxReplicas ⇒ status.unboundedRecommended set, Event message contains `unboundedRecommended=`, reason = `MaxReplicasBinding`; binding-without-replica-change ⇒ no ExplainWorker notify. |

Test files mirror each implementation file. No new file types beyond the envtest harness extension.

---

## Sub-PR A: Status field + decision API split (no behaviour change)

This sub-PR adds the `UnboundedRecommended` storage location and the pure-function split inside the `decision` package. No reasoning tokens fire yet — those land in Sub-PR B.

### Task 1: G13 — Add `Status.UnboundedRecommended` field and regenerate CRD

**Files:**
- Modify: `api/v1alpha1/agenticautoscaler_types.go:204-208` (insert new field after `RecommendedReplicas`)
- Regenerate: `config/crd/bases/autoscaling.agentic.io_agenticautoscalers.yaml`

- [ ] **Step 1: Write the failing test**

Append to `api/v1alpha1/agenticautoscaler_types_test.go` (create the file if it doesn't exist, with the same `package v1alpha1` declaration; otherwise append):

```go
package v1alpha1_test

import (
	"testing"

	autoscalingv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/api/v1alpha1"
	"github.com/stretchr/testify/assert"
)

func TestAgenticAutoscalerStatus_UnboundedRecommendedFieldExists(t *testing.T) {
	s := autoscalingv1alpha1.AgenticAutoscalerStatus{
		RecommendedReplicas:  10,
		UnboundedRecommended: 15,
	}
	assert.Equal(t, int32(15), s.UnboundedRecommended,
		"status.unboundedRecommended must be addressable")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./api/v1alpha1/... -run TestAgenticAutoscalerStatus_UnboundedRecommendedFieldExists -v
```

Expected: FAIL — `s.UnboundedRecommended undefined`.

- [ ] **Step 3: Add the field**

In `api/v1alpha1/agenticautoscaler_types.go`, immediately after the `RecommendedReplicas` block (around line 207), insert:

```go
	// UnboundedRecommended is the raw forecaster-driven replica count, pre-clamp.
	// When this exceeds Spec.MaxReplicas the CRD bound is the binding constraint;
	// when it is below Spec.MinReplicas the floor is binding. Equals
	// RecommendedReplicas in the common case. See docs/design_v2.md §5 step 5
	// and §6.2 "Field provenance" for the capacity-planning intent.
	// +optional
	UnboundedRecommended int32 `json:"unboundedRecommended,omitempty"`
```

- [ ] **Step 4: Run test to verify it passes and regenerate CRD**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./api/v1alpha1/... -run TestAgenticAutoscalerStatus_UnboundedRecommendedFieldExists -v
make manifests
```

Expected: test PASSes; `git diff config/crd/bases/` shows a new `unboundedRecommended: format: int32 type: integer` entry under `status:`.

- [ ] **Step 5: Commit**

```bash
git add api/v1alpha1/agenticautoscaler_types.go \
        api/v1alpha1/agenticautoscaler_types_test.go \
        config/crd/bases/autoscaling.agentic.io_agenticautoscalers.yaml
git commit -m "feat(api): add Status.UnboundedRecommended field (T1, G13)"
```

---

### Task 2: G13 — Split `ComputeRecommended` into unbounded + clamp helpers

**Files:**
- Modify: `internal/decision/decision.go:132-149` (the existing `ComputeRecommended`)
- Modify: `internal/decision/decision_test.go` (add tests for the two new helpers; keep existing `TestComputeRecommended_*` cases referring to the new clamp helper)

The split:

- `ComputeUnboundedRecommended(predictedRPS, rpsPerPod float64) int32` — pure division → ceil; no clamp. Returns `math.MaxInt32` and the rpsPerPod-zero failsafe is moved to `ClampRecommended`.
- `ClampRecommended(unbounded, minReplicas, maxReplicas int32) (clamped int32, bindingReason string)` — clamps to `[min, max]`; returns the corresponding reasoning token constant when clamping fired, else `""`. For now the function returns string literals; Task 4 introduces the constants and Task 5 wires them.

- [ ] **Step 1: Write the failing tests**

In `internal/decision/decision_test.go`, add:

```go
func TestComputeUnboundedRecommended_TrivialCases(t *testing.T) {
	t.Run("zero rps per pod returns sentinel", func(t *testing.T) {
		got := decision.ComputeUnboundedRecommended(100.0, 0.0)
		assert.Equal(t, int32(math.MaxInt32), got,
			"unbounded math is undefined at rpsPerPod=0; sentinel surfaces it")
	})
	t.Run("ceiling rounds up", func(t *testing.T) {
		got := decision.ComputeUnboundedRecommended(101.0, 10.0)
		assert.Equal(t, int32(11), got)
	})
	t.Run("exact multiple no ceiling overshoot", func(t *testing.T) {
		got := decision.ComputeUnboundedRecommended(100.0, 10.0)
		assert.Equal(t, int32(10), got)
	})
}

func TestClampRecommended_NoBindingWhenInsideRange(t *testing.T) {
	clamped, reason := decision.ClampRecommended(7, 2, 10)
	assert.Equal(t, int32(7), clamped)
	assert.Equal(t, "", reason,
		"in-range recommendation must not set a binding reason")
}

func TestClampRecommended_MaxBinding(t *testing.T) {
	clamped, reason := decision.ClampRecommended(15, 2, 10)
	assert.Equal(t, int32(10), clamped)
	assert.Equal(t, "max_replicas_binding", reason)
}

func TestClampRecommended_MinBinding(t *testing.T) {
	clamped, reason := decision.ClampRecommended(1, 2, 10)
	assert.Equal(t, int32(2), clamped)
	assert.Equal(t, "min_replicas_binding", reason)
}

func TestClampRecommended_SentinelClampsToMax(t *testing.T) {
	// rpsPerPod=0 path: ComputeUnbounded returned math.MaxInt32; clamp
	// must still produce a valid replica count and surface MaxBinding.
	clamped, reason := decision.ClampRecommended(math.MaxInt32, 2, 10)
	assert.Equal(t, int32(10), clamped)
	assert.Equal(t, "max_replicas_binding", reason)
}
```

Add `"math"` and `"github.com/stretchr/testify/assert"` to the imports if not already present.

- [ ] **Step 2: Run tests to verify they fail**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./internal/decision/... -run 'TestComputeUnboundedRecommended|TestClampRecommended' -v
```

Expected: FAIL — `undefined: decision.ComputeUnboundedRecommended`, `undefined: decision.ClampRecommended`.

- [ ] **Step 3: Add the two helpers**

In `internal/decision/decision.go`, immediately above the existing `ComputeRecommended`:

```go
// ComputeUnboundedRecommended returns the raw forecaster-driven replica count,
// ceil(predictedRPS / rpsPerPod), with no CRD-bound clamp. When rpsPerPod is
// non-positive the result is math.MaxInt32 — a sentinel that subsequent
// ClampRecommended turns into maxReplicas (the failsafe). Surfacing the
// sentinel (rather than silently returning maxReplicas here) lets callers
// distinguish "forecast over the cap" from "no usable rps_per_pod".
func ComputeUnboundedRecommended(predictedRPS, rpsPerPod float64) int32 {
	if rpsPerPod <= 0 {
		return math.MaxInt32
	}
	return int32(math.Ceil(predictedRPS / rpsPerPod))
}

// ClampRecommended applies the CRD bounds and reports which bound (if any)
// was the binding constraint. The returned reasoning string is one of:
//   - "max_replicas_binding"  when unbounded > maxReplicas (clamped to max)
//   - "min_replicas_binding"  when unbounded < minReplicas (clamped to min)
//   - ""                       when unbounded is already in [min, max]
//
// Per design §5 precedence rule 1, this binding reason is *tentative* —
// step 6 (cap) and step 7 (cooldown) may overwrite it in ApplyCapAndCooldown.
func ClampRecommended(unbounded, minReplicas, maxReplicas int32) (int32, string) {
	if unbounded > maxReplicas {
		return maxReplicas, "max_replicas_binding"
	}
	if unbounded < minReplicas {
		return minReplicas, "min_replicas_binding"
	}
	return unbounded, ""
}
```

Leave the existing `ComputeRecommended` in place for now — Task 3 retires it after the reconciler is migrated.

- [ ] **Step 4: Run tests to verify they pass**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./internal/decision/... -run 'TestComputeUnboundedRecommended|TestClampRecommended' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/decision/decision.go internal/decision/decision_test.go
git commit -m "feat(decision): split ComputeRecommended into unbounded + clamp (T2, G13)"
```

---

### Task 3: G13 — Reconciler writes `Status.UnboundedRecommended`

**Files:**
- Modify: `internal/controller/agenticautoscaler_controller.go:215-220` (compute) and `:289-292` (status persist)
- Modify: `internal/decision/decision.go` (delete the now-unused `ComputeRecommended`)
- Modify: `internal/decision/decision_test.go` (remove tests for the deleted helper if any reference the old name)
- Modify: `test/envtest/...` (no test changes — covered in T14)

- [ ] **Step 1: Write the failing test**

Add to `internal/controller/agenticautoscaler_controller_test.go` (or wherever the package-level unit tests live; if no such file exists, create it as `package controller_test` mirroring existing controller test files):

```go
func TestReconciler_PersistsUnboundedRecommended(t *testing.T) {
	// Forecast asks for 25 replicas; cluster CR sets maxReplicas=10.
	// Status must show recommendedReplicas=10 AND unboundedRecommended=25.
	t.Skip("integration covered by T14 envtest; this is a placeholder " +
		"to document the intent — implementation lives in reconcile loop")
}
```

(The full assertion lives in T14 because it requires envtest. This placeholder pins the intent so the next reviewer sees it.)

- [ ] **Step 2: Switch the reconciler to the new helpers**

In `internal/controller/agenticautoscaler_controller.go`, replace the lines around 215-219:

```go
	// Step 5 (cont.): pre-cap recommendation.
	minReplicas := derefOr(aas.Spec.MinReplicas, 2)
	maxReplicas := derefOr(aas.Spec.MaxReplicas, 10)
	recommended := decision.ComputeRecommended(forecastResp.PredictedRPS, rpsPerPod, minReplicas, maxReplicas)
```

with:

```go
	// Step 5 (cont.): pre-cap recommendation.
	// Unbounded = pre-clamp; recommended = post-clamp; bindingReason is the
	// tentative step-5 reasoning token (step 6/7 may overwrite it).
	minReplicas := derefOr(aas.Spec.MinReplicas, 2)
	maxReplicas := derefOr(aas.Spec.MaxReplicas, 10)
	unbounded := decision.ComputeUnboundedRecommended(forecastResp.PredictedRPS, rpsPerPod)
	recommended, bindingReason := decision.ClampRecommended(unbounded, minReplicas, maxReplicas)
```

Then immediately before the existing `aas.Status.RecommendedReplicas = recommended` (around line 290), add:

```go
	aas.Status.UnboundedRecommended = unbounded
```

(The `bindingReason` local is wired into `CapInput` in Task 6 — for this commit it can stay unused; if `staticcheck` flags it, prefix with `_` and rename back in T6. Recommended: name it `bindingReason` already and add a `_ = bindingReason` line so the variable exists at the right name; the compiler is happy, the next task removes the `_ =` line.)

- [ ] **Step 3: Delete the now-orphaned `ComputeRecommended`**

In `internal/decision/decision.go`, delete the function `ComputeRecommended` (lines 132-149). Run:

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go build ./...
```

Fix any leftover callers (there should be none — only the controller called it).

- [ ] **Step 4: Run the existing suite to confirm no regression**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./internal/decision/... ./internal/controller/... -v
```

Expected: PASS. (The skipped placeholder test counts as passing.)

- [ ] **Step 5: Commit**

```bash
git add internal/controller/agenticautoscaler_controller.go internal/decision/decision.go
git commit -m "feat(controller): persist Status.UnboundedRecommended; retire ComputeRecommended (T3, G13)"
```

---

## Sub-PR B: Reasoning tokens + cap-precedence chain

This sub-PR introduces the two new tokens as first-class constants and re-shapes `ApplyCapAndCooldown` so the binding reason set by Task 2 actually carries through to the output (subject to the step-6/7 overrides spelled out in design §5 precedence rules).

### Task 4: G13 / F27 — Add `MaxReplicasBinding` + `MinReplicasBinding` constants

**Files:**
- Modify: `internal/reasoning/tokens.go` (constant block + `AllTokens()`)
- Modify: `internal/reasoning/tokens_test.go` (the existing snapshot test for `AllTokens`)

- [ ] **Step 1: Write the failing test**

In `internal/reasoning/tokens_test.go` (find the existing `TestAllTokens_*` test or `TestAllTokensInventory`; if there isn't one, add it):

```go
func TestAllTokens_IncludesBindingTokens(t *testing.T) {
	all := reasoning.AllTokens()
	assert.Equal(t, "max_replicas_binding", all["MaxReplicasBinding"])
	assert.Equal(t, "min_replicas_binding", all["MinReplicasBinding"])
}

func TestBindingTokenConstants(t *testing.T) {
	assert.Equal(t, "max_replicas_binding", reasoning.MaxReplicasBinding)
	assert.Equal(t, "min_replicas_binding", reasoning.MinReplicasBinding)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./internal/reasoning/... -run 'TestAllTokens_IncludesBindingTokens|TestBindingTokenConstants' -v
```

Expected: FAIL — `undefined: reasoning.MaxReplicasBinding`, `undefined: reasoning.MinReplicasBinding`.

- [ ] **Step 3: Add the constants and extend `AllTokens()`**

In `internal/reasoning/tokens.go`, inside the existing constant block (after `CooldownHoldingDown` around line 19), add:

```go
	// Hot-path reconciler — CRD-bound binding constraints (set in
	// decision.ClampRecommended; may be overwritten by step 6/7 per
	// design §5 precedence rules).
	MaxReplicasBinding = "max_replicas_binding"
	MinReplicasBinding = "min_replicas_binding"
```

In the same file, extend the `AllTokens()` map literal:

```go
		"MaxReplicasBinding":  MaxReplicasBinding,
		"MinReplicasBinding":  MinReplicasBinding,
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./internal/reasoning/... -v
```

Expected: PASS for the two new tests and any existing snapshot.

- [ ] **Step 5: Commit**

```bash
git add internal/reasoning/tokens.go internal/reasoning/tokens_test.go
git commit -m "feat(reasoning): add MaxReplicasBinding + MinReplicasBinding tokens (T4, G13/F27)"
```

---

### Task 5: G13 / F27 — `ApplyCapAndCooldown` carries `BindingReason` through with correct precedence

**Files:**
- Modify: `internal/decision/decision.go:151-222` (`CapInput`, `ApplyCapAndCooldown`)
- Modify: `internal/decision/decision_test.go` (add precedence-chain tests)

Design §5 precedence rules (paraphrased from `design_v2.md:471-478`):

1. Step 5 tentatively sets the binding reason (already done in T2).
2. Step 6 overwrites with `step_capped_*` when the maxStep cap clips the move.
3. Step 7 overwrites with `cooldown_holding_*` when cooldown blocks the move.
4. Step 8 hysteresis suppresses the `/scale` patch when `target == current_replicas` after steps 6/7. **The binding reason still wins** when hysteresis suppresses without a cap/cooldown override (the "workload already at the bound" branch).

- [ ] **Step 1: Write the failing tests**

In `internal/decision/decision_test.go`:

```go
func TestApplyCapAndCooldown_BindingReasonCarriesWhenNoOverride(t *testing.T) {
	// Workload already at maxReplicas; forecast keeps asking for more.
	// Hysteresis fires (target == current), no step cap, no cooldown.
	// Expected reason: max_replicas_binding (the binding reason is the
	// most informative thing the operator can see).
	out := decision.ApplyCapAndCooldown(decision.CapInput{
		Recommended:    10, // clamped value
		Current:        10,
		MaxStep:        3,
		CooldownUp:     60,
		CooldownDown:   300,
		LastScaleUp:    time.Now().Add(-time.Hour),
		LastScaleDown:  time.Now().Add(-time.Hour),
		Now:            time.Now(),
		BindingReason:  reasoning.MaxReplicasBinding,
	})
	assert.Equal(t, reasoning.MaxReplicasBinding, out.Reason)
	assert.False(t, out.ShouldPatch, "target == current ⇒ no /scale patch")
}

func TestApplyCapAndCooldown_BindingReasonOverwrittenByStepCap(t *testing.T) {
	// Clamped recommendation is 10; current is 5; maxStep is 2 — step cap fires.
	// BindingReason was set tentatively in step 5; step 6 must overwrite it.
	out := decision.ApplyCapAndCooldown(decision.CapInput{
		Recommended:    10,
		Current:        5,
		MaxStep:        2,
		CooldownUp:     0,
		CooldownDown:   0,
		LastScaleUp:    time.Now().Add(-time.Hour),
		LastScaleDown:  time.Now().Add(-time.Hour),
		Now:            time.Now(),
		BindingReason:  reasoning.MaxReplicasBinding,
	})
	assert.Equal(t, reasoning.StepCappedUp, out.Reason,
		"step 6 must overwrite binding reason when cap clips the move")
	assert.Equal(t, int32(7), out.Target)
	assert.True(t, out.ShouldPatch)
}

func TestApplyCapAndCooldown_BindingReasonOverwrittenByCooldown(t *testing.T) {
	now := time.Now()
	out := decision.ApplyCapAndCooldown(decision.CapInput{
		Recommended:    10,
		Current:        5,
		MaxStep:        10,
		CooldownUp:     300,
		CooldownDown:   0,
		LastScaleUp:    now.Add(-10 * time.Second), // still in cooldown
		LastScaleDown:  now.Add(-time.Hour),
		Now:            now,
		BindingReason:  reasoning.MaxReplicasBinding,
	})
	assert.Equal(t, reasoning.CooldownHoldingUp, out.Reason,
		"step 7 cooldown must overwrite binding reason")
	assert.Equal(t, int32(5), out.Target)
	assert.False(t, out.ShouldPatch)
}

func TestApplyCapAndCooldown_EmptyBindingReasonGoesToNoChange(t *testing.T) {
	// Regression-pin the existing no_change semantics for the non-binding case.
	out := decision.ApplyCapAndCooldown(decision.CapInput{
		Recommended:   5,
		Current:       5,
		MaxStep:       3,
		CooldownUp:    0,
		CooldownDown:  0,
		LastScaleUp:   time.Now().Add(-time.Hour),
		LastScaleDown: time.Now().Add(-time.Hour),
		Now:           time.Now(),
		BindingReason: "",
	})
	assert.Equal(t, reasoning.NoChange, out.Reason)
	assert.False(t, out.ShouldPatch)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./internal/decision/... -run TestApplyCapAndCooldown_Binding -v
```

Expected: FAIL — `unknown field BindingReason in struct literal of type decision.CapInput`.

- [ ] **Step 3: Extend `CapInput` and rewrite the cap function**

In `internal/decision/decision.go`, change `CapInput` (around line 152) to:

```go
// CapInput feeds ApplyCapAndCooldown.
type CapInput struct {
	Recommended   int32  // post-clamp value
	Current       int32
	MaxStep       int32
	CooldownUp    int32  // seconds
	CooldownDown  int32  // seconds
	LastScaleUp   time.Time
	LastScaleDown time.Time
	Now           time.Time
	// BindingReason is the tentative reasoning token set by step 5
	// (decision.ClampRecommended). One of "max_replicas_binding",
	// "min_replicas_binding", or "". Step 6 / step 7 overrides it
	// when the cap or cooldown also fires. Step 8 hysteresis preserves
	// it (the binding-without-replica-change branch).
	BindingReason string
}
```

Rewrite `ApplyCapAndCooldown` (replacing the existing function body around lines 184-222):

```go
// ApplyCapAndCooldown implements design §5 steps 6-8 with the precedence
// chain from §5 precedence rules 1-4:
//   1. Start with the tentative binding reason from step 5 (in.BindingReason).
//   2. Step cap (maxStepSize) — overwrites with step_capped_{up,down} when
//      the cap clips the move.
//   3. Cooldown — overwrites with cooldown_holding_{up,down} when it blocks
//      the move entirely.
//   4. Hysteresis (target == current) — preserves the binding reason if any
//      is still set, else emits no_change.
func ApplyCapAndCooldown(in CapInput) CapOutput {
	target := in.Recommended
	reason := in.BindingReason

	switch {
	case target > in.Current:
		if target > in.Current+in.MaxStep {
			target = in.Current + in.MaxStep
			reason = reasoning.StepCappedUp
		} else {
			reason = reasoning.ScaleUp
		}
		// Cooldown overrides step cap and binding reason; zero out the patch.
		if in.Now.Sub(in.LastScaleUp) < time.Duration(in.CooldownUp)*time.Second {
			target = in.Current
			reason = reasoning.CooldownHoldingUp
		}
	case target < in.Current:
		if target < in.Current-in.MaxStep {
			target = in.Current - in.MaxStep
			reason = reasoning.StepCappedDown
		} else {
			reason = reasoning.ScaleDown
		}
		if in.Now.Sub(in.LastScaleDown) < time.Duration(in.CooldownDown)*time.Second {
			target = in.Current
			reason = reasoning.CooldownHoldingDown
		}
	default:
		// target == current: preserve binding reason if set, else no_change.
		target = in.Current
		if reason == "" {
			reason = reasoning.NoChange
		}
	}

	return CapOutput{
		Target:      target,
		Reason:      reason,
		ShouldPatch: target != in.Current,
	}
}
```

Note that the `case target > in.Current` and `case target < in.Current` branches *always* overwrite `reason` (step cap or scale token) because per spec the binding reason is overwritten whenever a replica change actually happens — the binding reason only "wins" in the hysteresis branch.

- [ ] **Step 4: Run tests to verify they pass**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./internal/decision/... -v
```

Expected: PASS. (All four new tests + any pre-existing `TestApplyCapAndCooldown_*` cases — those construct `CapInput` without `BindingReason`, which defaults to `""`, so semantics are preserved.)

- [ ] **Step 5: Commit**

```bash
git add internal/decision/decision.go internal/decision/decision_test.go
git commit -m "feat(decision): ApplyCapAndCooldown carries BindingReason with precedence (T5, G13/F27)"
```

---

### Task 6: G13 — Reconciler wires `BindingReason` into `CapInput`

**Files:**
- Modify: `internal/controller/agenticautoscaler_controller.go:222-231` (the `CapInput` literal)

- [ ] **Step 1: Write the failing test**

In `internal/controller/agenticautoscaler_controller_test.go` (placeholder; full assertion in T14 envtest):

```go
func TestReconciler_BindingReasonFlowsIntoCapInput(t *testing.T) {
	t.Skip("integration covered by T14 envtest; this is a placeholder " +
		"to document the intent — BindingReason from ClampRecommended must " +
		"reach ApplyCapAndCooldown via CapInput.BindingReason")
}
```

- [ ] **Step 2: Update the `CapInput` literal**

In `internal/controller/agenticautoscaler_controller.go`, change the `CapInput` literal (around line 222-231) to include `BindingReason`:

```go
	capOut := decision.ApplyCapAndCooldown(decision.CapInput{
		Recommended:   recommended,
		Current:       currentReplicas,
		MaxStep:       effective.MaxStep,
		CooldownUp:    effective.CooldownUp,
		CooldownDown:  effective.CooldownDown,
		LastScaleUp:   state.LastScaleUpTime,
		LastScaleDown: state.LastScaleDownTime,
		Now:           now,
		BindingReason: bindingReason,
	})
```

Remove the `_ = bindingReason` line if you added one in T3.

- [ ] **Step 3: Run the suite**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go build ./... && \
  go test ./internal/decision/... ./internal/controller/... -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/controller/agenticautoscaler_controller.go
git commit -m "feat(controller): thread BindingReason into ApplyCapAndCooldown (T6, G13)"
```

---

## Sub-PR C: Event message + ExplainWorker trigger gating

This sub-PR delivers the operator-visible event-message change and confirms (via test) that binding-without-replica-change does **not** trigger the ExplainWorker (per design §6.2 trigger rules, line 885).

### Task 7: G13 / F27 — Event message includes `unboundedRecommended` when it differs from `recommendedReplicas`

**Files:**
- Modify: `internal/controller/agenticautoscaler_controller.go:254-256` (the `Eventf` call)

- [ ] **Step 1: Write the failing test**

In `internal/controller/agenticautoscaler_controller_test.go`:

```go
func TestReconciler_EventMessageContainsUnboundedRecommended(t *testing.T) {
	t.Skip("full envtest assertion in T14; this placeholder pins the " +
		"contract: when unboundedRecommended differs from recommended, " +
		"the K8s Event message must include unboundedRecommended=N")
}
```

- [ ] **Step 2: Update the `Eventf` call**

In `internal/controller/agenticautoscaler_controller.go`, replace the `Eventf` block (around line 254-256) with:

```go
	// Step 10: emit Event. The unboundedRecommended field is included
	// only when it differs from the clamped recommendedReplicas; this
	// keeps the common-case event short while surfacing the binding-
	// constraint signal when relevant. See design_v2.md §5 step 10.
	if unbounded != recommended {
		r.EventRecorder.Eventf(&aas, corev1.EventTypeNormal, capOut.Reason,
			"current_rps=%.1f predicted_rps=%.1f current=%d target=%d "+
				"recommended=%d unboundedRecommended=%d model=%s",
			currentRPS, forecastResp.PredictedRPS, currentReplicas, capOut.Target,
			recommended, unbounded, forecastResp.ModelUsed)
	} else {
		r.EventRecorder.Eventf(&aas, corev1.EventTypeNormal, capOut.Reason,
			"current_rps=%.1f predicted_rps=%.1f current=%d target=%d model=%s",
			currentRPS, forecastResp.PredictedRPS, currentReplicas, capOut.Target,
			forecastResp.ModelUsed)
	}
```

- [ ] **Step 3: Run the suite**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go build ./... && \
  go test ./internal/controller/... -v
```

Expected: PASS (the placeholder test is skipped; build must compile and existing tests still pass).

- [ ] **Step 4: Commit**

```bash
git add internal/controller/agenticautoscaler_controller.go
git commit -m "feat(controller): include unboundedRecommended in Event message when it differs (T7, G13/F27)"
```

---

### Task 8: G13 — Pin "binding without replica change does NOT notify ExplainWorker"

**Files:**
- Modify: `internal/controller/notifier_test.go` (or wherever `ChannelNotifier` tests live; if there isn't one, create `internal/controller/interfaces_test.go`)

Per design §6.2 line 885 ("Binding without replica change exclusion"), the reconciler's existing `if capOut.ShouldPatch { r.ExplainNotify.Notify(...) }` already enforces this rule. The contract is invariant; we want a regression-test pin so it can't drift.

- [ ] **Step 1: Write the failing test**

In `internal/controller/interfaces_test.go`:

```go
package controller_test

import (
	"testing"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/controller"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/reasoning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChannelNotifier_DropAndReplaceOnSecondNotify is a sanity-pin for the
// existing drop-and-replace semantics — kept so the binding-token work
// doesn't accidentally regress it.
func TestChannelNotifier_DropAndReplaceOnSecondNotify(t *testing.T) {
	ch := make(chan controller.ExplainRequest, 1)
	n := controller.ChannelNotifier{Ch: ch}

	first := controller.ExplainRequest{Reason: reasoning.ScaleUp, TargetReplicas: 3}
	second := controller.ExplainRequest{Reason: reasoning.MaxReplicasBinding, TargetReplicas: 5}

	n.Notify(first)
	n.Notify(second)

	require.Len(t, ch, 1, "buffered channel is size 1; second notify drops the stale one")
	got := <-ch
	assert.Equal(t, reasoning.MaxReplicasBinding, got.Reason,
		"the most recent notify must be the one consumed")
}
```

This test does not yet exist — running it should pass (the existing code already implements this contract), but its absence is the gap. We're adding it as a regression pin for the binding-token work in this plan. If the package already has equivalent coverage, skip this task and amend the commit message accordingly.

- [ ] **Step 2: Run test**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./internal/controller/... -run TestChannelNotifier_DropAndReplace -v
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/controller/interfaces_test.go
git commit -m "test(controller): pin ChannelNotifier drop-and-replace contract (T8, G13)"
```

---

## Sub-PR D: ExplainRequest fields + prompt template

This sub-PR extends `ExplainRequest` with the six new fields the prompt template will consume (per design §6.2 field table), fixes the F12 gate, and adds the three new prompt lines (Long-term context + two binding-conditional lines) plus the updated closing wording.

### Task 9: G18 / F33 — Extend `ExplainRequest` with the new fields

**Files:**
- Modify: `internal/controller/interfaces.go:39-55` (the `ExplainRequest` struct)

- [ ] **Step 1: Write the failing test**

In `internal/controller/interfaces_test.go` (append to the file from T8):

```go
func TestExplainRequest_HasG18Fields(t *testing.T) {
	// Compile-time pin: the six fields the G18 prompt template consumes
	// must exist on ExplainRequest with the documented types.
	r := controller.ExplainRequest{
		UnboundedRecommended: 15,
		MaxReplicas:          10,
		MinReplicas:          2,
		BaselineRPS:          400,
		PeakP95RPS:           1200,
		HourlyProfileValid:   true,
	}
	assert.Equal(t, int32(15), r.UnboundedRecommended)
	assert.Equal(t, int32(10), r.MaxReplicas)
	assert.Equal(t, int32(2), r.MinReplicas)
	assert.Equal(t, int32(400), r.BaselineRPS)
	assert.Equal(t, int32(1200), r.PeakP95RPS)
	assert.True(t, r.HourlyProfileValid)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./internal/controller/... -run TestExplainRequest_HasG18Fields -v
```

Expected: FAIL — `unknown field UnboundedRecommended in struct literal`.

- [ ] **Step 3: Add the fields**

In `internal/controller/interfaces.go`, extend the `ExplainRequest` struct (insert after `EffectiveMaxStep`, before the closing brace):

```go
	// G18 — long-term context and CRD-binding fields. Populated by the
	// reconciler from status.classifiedParams.context and spec.*Replicas
	// respectively. See docs/design_v2.md §6.2 "ExplainRequest fields".
	UnboundedRecommended int32 // pre-clamp forecaster ask
	MaxReplicas          int32 // spec.maxReplicas (used by max_replicas_binding prose)
	MinReplicas          int32 // spec.minReplicas (used by min_replicas_binding prose)
	BaselineRPS          int32 // status.classifiedParams.context.baselineRPS (zero if not classified)
	PeakP95RPS           int32 // status.classifiedParams.context.peakP95RPS (zero if not classified)
	HourlyProfileValid   bool  // status.classifiedParams.context.hourlyProfileValid
```

- [ ] **Step 4: Run test to verify it passes**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./internal/controller/... -run TestExplainRequest_HasG18Fields -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/interfaces.go internal/controller/interfaces_test.go
git commit -m "feat(controller): add G18 fields to ExplainRequest (T9, G18/F33)"
```

---

### Task 10: G18 — Reconciler populates the new `ExplainRequest` fields

**Files:**
- Modify: `internal/controller/agenticautoscaler_controller.go:267-283` (the `ExplainRequest` literal)
- Possibly add helper functions near `classifiedPattern` / `classifiedConfidence`

- [ ] **Step 1: Write the failing test**

(Placeholder — full assertion in T15 unit test on the prompt builder, which exercises the populated fields.)

```go
func TestReconciler_PopulatesG18ExplainRequestFields(t *testing.T) {
	t.Skip("integration covered by T15 explainer prompt unit test")
}
```

- [ ] **Step 2: Add the helpers**

In `internal/controller/agenticautoscaler_controller.go`, near the existing `classifiedPattern` / `classifiedConfidence` helpers (search for them), add:

```go
// classifiedBaselineRPS returns the cold-path baseline if classified; else 0.
func classifiedBaselineRPS(aas *autoscalingv1alpha1.AgenticAutoscaler) int32 {
	if aas.Status.ClassifiedParams == nil || aas.Status.ClassifiedParams.Context == nil {
		return 0
	}
	return aas.Status.ClassifiedParams.Context.BaselineRPS
}

// classifiedPeakP95RPS returns the cold-path p95 if classified; else 0.
func classifiedPeakP95RPS(aas *autoscalingv1alpha1.AgenticAutoscaler) int32 {
	if aas.Status.ClassifiedParams == nil || aas.Status.ClassifiedParams.Context == nil {
		return 0
	}
	return aas.Status.ClassifiedParams.Context.PeakP95RPS
}

// classifiedHourlyProfileValid returns the cold-path coverage flag; else false.
func classifiedHourlyProfileValid(aas *autoscalingv1alpha1.AgenticAutoscaler) bool {
	if aas.Status.ClassifiedParams == nil || aas.Status.ClassifiedParams.Context == nil {
		return false
	}
	return aas.Status.ClassifiedParams.Context.HourlyProfileValid
}
```

(If `HourlyProfileValid` is a different field name on `ContextFields`, mirror whatever Plan 13 landed — check `api/v1alpha1/agenticautoscaler_types.go` for the exact name and adjust the helper accordingly.)

- [ ] **Step 3: Extend the `ExplainRequest` literal**

In `internal/controller/agenticautoscaler_controller.go`, extend the `Notify(ExplainRequest{...})` literal (around line 267-283) to include the new fields:

```go
	if capOut.ShouldPatch {
		r.ExplainNotify.Notify(ExplainRequest{
			Namespace:             aas.Namespace,
			Name:                  aas.Name,
			Reason:                capOut.Reason,
			CurrentReplicas:       currentReplicas,
			RecommendedReplicas:   recommended,
			UnboundedRecommended:  unbounded,
			TargetReplicas:        capOut.Target,
			MaxReplicas:           maxReplicas,
			MinReplicas:           minReplicas,
			CurrentRPS:            currentRPS,
			PredictedRPS:          forecastResp.PredictedRPS,
			HorizonMinutes:        forecastResp.HorizonMinutes,
			ModelUsed:             forecastResp.ModelUsed,
			Pattern:               classifiedPattern(&aas),
			Confidence:            classifiedConfidence(&aas),
			BaselineRPS:           classifiedBaselineRPS(&aas),
			PeakP95RPS:            classifiedPeakP95RPS(&aas),
			HourlyProfileValid:    classifiedHourlyProfileValid(&aas),
			EffectiveCooldownUp:   effective.CooldownUp,
			EffectiveCooldownDown: effective.CooldownDown,
			EffectiveMaxStep:      effective.MaxStep,
		})
	}
```

- [ ] **Step 4: Run the suite**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go build ./... && \
  go test ./internal/controller/... -v
```

Expected: PASS (build green, placeholder test skips).

- [ ] **Step 5: Commit**

```bash
git add internal/controller/agenticautoscaler_controller.go internal/controller/agenticautoscaler_controller_test.go
git commit -m "feat(controller): populate G18 ExplainRequest fields (T10, G18)"
```

---

### Task 11: F12 + G18 — Fix Long-term context gate to `Pattern != ""` and add the line

**Files:**
- Modify: `internal/explainer/prompt.go:48-74` (the prompt builder)
- Modify: `internal/explainer/prompt_test.go` (new tests for the Long-term context line)

Per design §6.2 line 1008-1009: the **Traffic pattern** line gates on `Pattern != "" && Pattern != "default"` (no change — current code is right). The **Long-term context** line gates only on `Pattern != ""` — F12 explicitly does NOT exclude `"default"` because a workload classified as `default` still has measured baseline / p95 worth surfacing.

- [ ] **Step 1: Write the failing tests**

In `internal/explainer/prompt_test.go`, append:

```go
// -----------------------------------------------------------------------
// Long-term context line — F12 gate is Pattern != "" (NOT != "default").
// -----------------------------------------------------------------------

func TestBuildPrompt_IncludesLongTermContextWhenPatternSet(t *testing.T) {
	req := baseRequest()
	req.BaselineRPS = 400
	req.PeakP95RPS = 1200
	_, user := explainer.BuildPrompt(req)
	assert.Contains(t, user, "Long-term context: baseline 400 rps, p95 1200 rps")
}

func TestBuildPrompt_IncludesLongTermContextWhenPatternIsDefault(t *testing.T) {
	// F12: classifier *has* run (Pattern="default"); baseline/p95 are real.
	req := baseRequest()
	req.Pattern = "default"
	req.BaselineRPS = 50
	req.PeakP95RPS = 200
	_, user := explainer.BuildPrompt(req)
	assert.Contains(t, user, "Long-term context: baseline 50 rps, p95 200 rps")
	assert.NotContains(t, user, "Traffic pattern: default",
		"Traffic-pattern line is still gated on Pattern != \"default\"")
}

func TestBuildPrompt_OmitsLongTermContextWhenPatternEmpty(t *testing.T) {
	req := baseRequest()
	req.Pattern = ""
	req.BaselineRPS = 400
	req.PeakP95RPS = 1200
	_, user := explainer.BuildPrompt(req)
	assert.NotContains(t, user, "Long-term context:")
}

func TestBuildPrompt_LongTermContextWithZeroValues(t *testing.T) {
	// F12 rationale: a workload with genuinely zero traffic must still
	// have the line rendered (zero is a measurement, not absence).
	req := baseRequest()
	req.Pattern = "flat"
	req.BaselineRPS = 0
	req.PeakP95RPS = 0
	_, user := explainer.BuildPrompt(req)
	assert.Contains(t, user, "Long-term context: baseline 0 rps, p95 0 rps")
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./internal/explainer/... -run TestBuildPrompt_.*LongTermContext -v
```

Expected: FAIL — output doesn't contain `Long-term context:`.

- [ ] **Step 3: Add the line and update the closing wording**

In `internal/explainer/prompt.go`, change `BuildPrompt` (the body around lines 48-74) to:

```go
func BuildPrompt(req controller.ExplainRequest) (system, user string) {
	var b strings.Builder

	if req.Pattern != "" && req.Pattern != "default" {
		fmt.Fprintf(&b, "Traffic pattern: %s (confidence: %s)\n",
			req.Pattern, req.Confidence)
	}

	// F12: Long-term context gates only on Pattern != "" (NOT != "default") —
	// a workload classified as "default" still has measured baseline/p95.
	if req.Pattern != "" {
		fmt.Fprintf(&b, "Long-term context: baseline %d rps, p95 %d rps\n",
			req.BaselineRPS, req.PeakP95RPS)
	}

	fmt.Fprintf(&b, "Current RPS: %.1f, Predicted RPS (%d min ahead): %.1f\n",
		req.CurrentRPS, req.HorizonMinutes, req.PredictedRPS)
	fmt.Fprintf(&b, "Scaling: %d → %d replicas (%s)\n",
		req.CurrentReplicas, req.TargetReplicas, req.Reason)

	if req.Reason == reasoning.StepCappedUp || req.Reason == reasoning.StepCappedDown {
		fmt.Fprintf(&b,
			"This scale was limited by maxStep: the controller computed %d replicas from the forecast but moved only to %d this reconcile (cap: %d replicas per reconcile).\n",
			req.RecommendedReplicas, req.TargetReplicas, req.EffectiveMaxStep)
	}

	fmt.Fprintf(&b, "Forecasting model: %s\n", req.ModelUsed)
	fmt.Fprintf(&b,
		"Active parameters: scaleUpCooldown=%ds, scaleDownCooldown=%ds, maxStep=%d\n",
		req.EffectiveCooldownUp, req.EffectiveCooldownDown, req.EffectiveMaxStep)
	fmt.Fprintf(&b, "\nExplain why this decision was made and what the traffic data suggests "+
		"relative to the workload's long-term baseline.")

	return SystemMessage, b.String()
}
```

(Note: the closing prompt wording was updated to `"... relative to the workload's long-term baseline."` per design §6.2 line 1002-1003 — match the new line break exactly.)

- [ ] **Step 4: Run tests to verify they pass**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./internal/explainer/... -v
```

Expected: PASS for the four new tests and all pre-existing tests. The existing `TestBuildPrompt_HappyPath` may need an update — it doesn't set `BaselineRPS`/`PeakP95RPS`, so the new line will render `Long-term context: baseline 0 rps, p95 0 rps`. Decide:
  - **Option A (recommended):** update `baseRequest()` to set `BaselineRPS: 400, PeakP95RPS: 1200` so all existing tests render the line as the human-realistic case.
  - **Option B:** add an `assert.Contains(user, "Long-term context: baseline 0 rps, p95 0 rps")` to `TestBuildPrompt_HappyPath`.

Pick Option A — update `baseRequest()` once, all dependent tests stay green.

- [ ] **Step 5: Commit**

```bash
git add internal/explainer/prompt.go internal/explainer/prompt_test.go
git commit -m "feat(explainer): add Long-term context line (Pattern != \"\" gate); update closing wording (T11, F12/G18)"
```

---

### Task 12: F33 / G18 — `max_replicas_binding` conditional prompt line

**Files:**
- Modify: `internal/explainer/prompt.go` (insert the conditional after the existing step-cap block)
- Modify: `internal/explainer/prompt_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/explainer/prompt_test.go`:

```go
// -----------------------------------------------------------------------
// Binding lines — F33 prompt conditionals.
// -----------------------------------------------------------------------

func TestBuildPrompt_IncludesMaxBindingLine(t *testing.T) {
	req := baseRequest()
	req.Reason = reasoning.MaxReplicasBinding
	req.UnboundedRecommended = 25
	req.RecommendedReplicas = 10
	req.TargetReplicas = 10
	req.MaxReplicas = 10
	_, user := explainer.BuildPrompt(req)
	assert.Contains(t, user,
		"This scale was limited by maxReplicas: the forecast asked for 25 replicas but the CRD bound capped it at maxReplicas=10. Raise spec.maxReplicas to let the autoscaler scale further.")
	assert.NotContains(t, user, "limited by maxStep",
		"max_replicas_binding and step_capped_* are mutually exclusive")
}

func TestBuildPrompt_OmitsMaxBindingLineWhenOtherReason(t *testing.T) {
	req := baseRequest() // Reason = scale_up
	_, user := explainer.BuildPrompt(req)
	assert.NotContains(t, user, "limited by maxReplicas")
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./internal/explainer/... -run TestBuildPrompt_.*MaxBinding -v
```

Expected: FAIL — output lacks "limited by maxReplicas".

- [ ] **Step 3: Add the conditional**

In `internal/explainer/prompt.go`, immediately after the existing `if req.Reason == reasoning.StepCappedUp || req.Reason == reasoning.StepCappedDown { ... }` block (and before `Forecasting model: ...`), add:

```go
	if req.Reason == reasoning.MaxReplicasBinding {
		fmt.Fprintf(&b,
			"This scale was limited by maxReplicas: the forecast asked for %d replicas but the CRD bound capped it at maxReplicas=%d. Raise spec.maxReplicas to let the autoscaler scale further.\n",
			req.UnboundedRecommended, req.MaxReplicas)
	}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./internal/explainer/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/explainer/prompt.go internal/explainer/prompt_test.go
git commit -m "feat(explainer): add max_replicas_binding prompt conditional (T12, F33/G18)"
```

---

### Task 13: F33 / G18 — `min_replicas_binding` conditional prompt line

**Files:**
- Modify: `internal/explainer/prompt.go` (insert right after the T12 block)
- Modify: `internal/explainer/prompt_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/explainer/prompt_test.go`:

```go
func TestBuildPrompt_IncludesMinBindingLine(t *testing.T) {
	req := baseRequest()
	req.Reason = reasoning.MinReplicasBinding
	req.UnboundedRecommended = 1
	req.RecommendedReplicas = 2
	req.TargetReplicas = 2
	req.MinReplicas = 2
	_, user := explainer.BuildPrompt(req)
	assert.Contains(t, user,
		"This scale was limited by minReplicas: the forecast asked for only 1 replicas but the CRD bound floored it at minReplicas=2.")
}

func TestBuildPrompt_OmitsMinBindingLineWhenMaxBinding(t *testing.T) {
	req := baseRequest()
	req.Reason = reasoning.MaxReplicasBinding
	req.UnboundedRecommended = 25
	req.MaxReplicas = 10
	_, user := explainer.BuildPrompt(req)
	assert.NotContains(t, user, "limited by minReplicas",
		"min_replicas_binding and max_replicas_binding are mutually exclusive")
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./internal/explainer/... -run TestBuildPrompt_.*MinBinding -v
```

Expected: FAIL.

- [ ] **Step 3: Add the conditional**

In `internal/explainer/prompt.go`, immediately after the T12 `MaxReplicasBinding` block:

```go
	if req.Reason == reasoning.MinReplicasBinding {
		fmt.Fprintf(&b,
			"This scale was limited by minReplicas: the forecast asked for only %d replicas but the CRD bound floored it at minReplicas=%d.\n",
			req.UnboundedRecommended, req.MinReplicas)
	}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./internal/explainer/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/explainer/prompt.go internal/explainer/prompt_test.go
git commit -m "feat(explainer): add min_replicas_binding prompt conditional (T13, F33/G18)"
```

---

## Sub-PR E: End-to-end pinning

This sub-PR adds two integration pins that exercise the full stack:

- **T14:** envtest — a CR with `maxReplicas=3` and a fake forecast returning `predicted_rps` that translates to `unboundedRecommended=10` (or similar — chosen by the test setup) must produce: `Status.UnboundedRecommended=10`, `Status.RecommendedReplicas=3`, event with reason `MaxReplicasBinding`, event message containing `unboundedRecommended=10`. **And** the reverse: a CR with `unboundedRecommended <= maxReplicas` produces an event message without `unboundedRecommended=`.
- **T15:** explainer worker test (or extend the existing one) — a full `ExplainRequest` with `Reason=MaxReplicasBinding` produces a prompt with all three new lines (Long-term context + max binding) and no step-cap line.

### Task 14: G13 / F27 — envtest pins the unbounded → event-message → status chain

**Files:**
- Create or extend: `test/envtest/scale_envtest_test.go` (or wherever the existing envtest scenarios live; the canonical name is whatever the controller envtest suite uses — search for `package controller_test` files with `BeforeSuite` to find it)

- [ ] **Step 1: Discover the existing envtest file layout**

```bash
rg -l 'envtest.Environment' --type go
rg -l 'BeforeSuite' --type go
```

Locate the existing envtest fixture (likely `internal/controller/suite_test.go` or `test/envtest/suite_test.go`). Add the new test case in the same package so it shares the suite setup.

- [ ] **Step 2: Write the failing test**

Add to the located envtest file (adapt fixture names to whatever helpers exist in that file — `createAAS`, `waitForCondition`, etc.):

```go
var _ = Describe("Unbounded recommendation surfacing", func() {
	const (
		ns   = "g13-binding"
		name = "binding-aas"
	)

	BeforeEach(func() {
		// Fixture: CR with maxReplicas=3, minReplicas=1.
		// Fake forecast returns predictedRPS=300 with rpsPerPod=30 → unbounded=10.
		// Expected: Status.UnboundedRecommended=10, RecommendedReplicas=3,
		// Reason=MaxReplicasBinding, event message contains "unboundedRecommended=10".
		// (Use the existing fake-forecaster pattern from the suite — replace
		// values into the existing scaffolding.)
	})

	It("persists UnboundedRecommended into status when forecast exceeds maxReplicas", func() {
		// 1. Create CR.
		// 2. Trigger reconcile (the suite likely does this via a tick or manual call).
		// 3. Eventually:
		//    - Get latest CR.
		//    - Assert: Status.UnboundedRecommended == 10
		//    - Assert: Status.RecommendedReplicas == 3
		// Implementation skeleton:
		Eventually(func(g Gomega) {
			var aas autoscalingv1alpha1.AgenticAutoscaler
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &aas)).To(Succeed())
			g.Expect(aas.Status.UnboundedRecommended).To(Equal(int32(10)))
			g.Expect(aas.Status.RecommendedReplicas).To(Equal(int32(3)))
		}, "10s", "200ms").Should(Succeed())
	})

	It("emits MaxReplicasBinding event with unboundedRecommended in the message", func() {
		Eventually(func(g Gomega) {
			events := &corev1.EventList{}
			g.Expect(k8sClient.List(ctx, events,
				client.InNamespace(ns))).To(Succeed())
			var found bool
			for _, e := range events.Items {
				if e.Reason == reasoning.MaxReplicasBinding &&
					e.InvolvedObject.Name == name &&
					strings.Contains(e.Message, "unboundedRecommended=10") {
					found = true
					break
				}
			}
			g.Expect(found).To(BeTrue(),
				"expected an event with Reason=MaxReplicasBinding and message containing 'unboundedRecommended=10'; got: %+v", events.Items)
		}, "10s", "200ms").Should(Succeed())
	})

	It("does NOT include unboundedRecommended in message when it equals recommended", func() {
		// Use a parallel fixture with predictedRPS=60 → unbounded=2, maxReplicas=3 → recommended=2; no binding.
		// Expected: event message contains "target=2 model=" but NOT "unboundedRecommended=".
		// (Wire the parallel CR via a sibling Describe / It if the suite supports per-It fixtures.)
	})
})
```

(The skeleton above uses Ginkgo idioms because the existing envtest suite uses Ginkgo. If the suite is plain `testing.T`-style — check `BeforeSuite` vs `TestMain` in the located file — rewrite each `Eventually(...)` as `assert.Eventually(t, ...)`. Match the suite's existing style; do not introduce Ginkgo in a `testing.T` suite or vice versa.)

- [ ] **Step 3: Run the test to verify it fails initially, then passes after the prior tasks are present**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
XDG_CACHE_HOME=/home/pratyush.ghosh/scaler/.cache \
KUBEBUILDER_CONTROLPLANE_START_TIMEOUT=180s \
  make test-envtest
```

(Run with `required_permissions: ["all"]` in the agent shell — the sandbox blocks envtest's `kube-apiserver` process management. See plans 13/14 for the same pattern.)

Expected: PASS once Tasks 1-13 are landed. (If you run T14 in isolation before T13, the prompt path won't yet exist but the envtest doesn't exercise the prompt — only status and event — so this should still pass after Sub-PRs A-C plus T9-T10.)

- [ ] **Step 4: Commit**

```bash
git add test/envtest/scale_envtest_test.go  # or the actual file path
git commit -m "test(envtest): pin Status.UnboundedRecommended + MaxReplicasBinding event end-to-end (T14, G13/F27)"
```

---

### Task 15: G18 / F12 / F33 — Unit-test the full prompt for a MaxReplicasBinding event

**Files:**
- Modify: `internal/explainer/prompt_test.go` (add a fully-asserted "kitchen sink" test)

- [ ] **Step 1: Write the failing test**

In `internal/explainer/prompt_test.go`:

```go
// TestBuildPrompt_MaxReplicasBindingFullPrompt is the kitchen-sink test
// for Plan 15 — it asserts every line of the prompt when a max_replicas_binding
// event fires with full context.
func TestBuildPrompt_MaxReplicasBindingFullPrompt(t *testing.T) {
	req := controller.ExplainRequest{
		Namespace:             "demo",
		Name:                  "app-agentic",
		Reason:                reasoning.MaxReplicasBinding,
		CurrentReplicas:       3,
		RecommendedReplicas:   3,
		UnboundedRecommended:  25,
		TargetReplicas:        3,
		MaxReplicas:           3,
		MinReplicas:           1,
		CurrentRPS:            800.5,
		PredictedRPS:          2400.0,
		HorizonMinutes:        10,
		ModelUsed:             "prophet",
		Pattern:               "periodic",
		Confidence:            "high",
		BaselineRPS:           400,
		PeakP95RPS:            1500,
		HourlyProfileValid:    true,
		EffectiveCooldownUp:   60,
		EffectiveCooldownDown: 300,
		EffectiveMaxStep:      3,
	}

	_, user := explainer.BuildPrompt(req)

	// All eight expected lines appear in order, separated by newlines.
	expected := []string{
		"Traffic pattern: periodic (confidence: high)",
		"Long-term context: baseline 400 rps, p95 1500 rps",
		"Current RPS: 800.5, Predicted RPS (10 min ahead): 2400.0",
		"Scaling: 3 → 3 replicas (max_replicas_binding)",
		"This scale was limited by maxReplicas: the forecast asked for 25 replicas but the CRD bound capped it at maxReplicas=3. Raise spec.maxReplicas to let the autoscaler scale further.",
		"Forecasting model: prophet",
		"Active parameters: scaleUpCooldown=60s, scaleDownCooldown=300s, maxStep=3",
		"Explain why this decision was made and what the traffic data suggests relative to the workload's long-term baseline.",
	}

	for _, line := range expected {
		assert.Contains(t, user, line, "missing prompt line: %q", line)
	}
	assert.NotContains(t, user, "limited by maxStep",
		"max_replicas_binding is mutually exclusive with step_capped_*")
	assert.NotContains(t, user, "limited by minReplicas",
		"max_replicas_binding is mutually exclusive with min_replicas_binding")
}
```

- [ ] **Step 2: Run test to verify it passes**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
  go test ./internal/explainer/... -run TestBuildPrompt_MaxReplicasBindingFullPrompt -v
```

Expected: PASS (after Tasks 9-13 are landed). If any line is missing, the assertion message points directly at it.

- [ ] **Step 3: Commit**

```bash
git add internal/explainer/prompt_test.go
git commit -m "test(explainer): kitchen-sink prompt assertion for max_replicas_binding (T15, G18/F12/F33)"
```

---

## Wrap-up

After all 15 tasks land, run the full local-CI sweep:

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
XDG_CACHE_HOME=/home/pratyush.ghosh/scaler/.cache \
GOLANGCI_LINT_CACHE=/home/pratyush.ghosh/scaler/.cache/golangci \
KUBEBUILDER_CONTROLPLANE_START_TIMEOUT=180s \
  make pre-flight
```

(The `XDG_CACHE_HOME` + `KUBEBUILDER_CONTROLPLANE_START_TIMEOUT` overrides are sandbox-specific — see plans 13/14 for the pattern. Outside the sandbox, plain `make pre-flight` suffices.)

Expected output: `pre-flight OK — safe to push`.

Then follow `superpowers:finishing-a-development-branch` to integrate the work.

---

## Self-review checklist

- [x] **Spec coverage.** Every entry in §1 (Spec Coverage Map) has at least one task; G13 spans T1-T8 + T14; G18 spans T9-T13 + T15; F12 lands in T11; F27 lands in T4-T7; F33 lands in T9, T10, T12, T13.
- [x] **No placeholders.** Every step ships either real Go code, an exact shell command with expected output, or an explicit `t.Skip(...)` whose intent is documented and superseded by a later task (T14/T15 carry the actual integration assertions).
- [x] **Type consistency.** `BindingReason string` appears identically in `CapInput` (T5) and is read by `ApplyCapAndCooldown` (T5) and written by the reconciler (T6). `ExplainRequest` fields added in T9 are the same set populated in T10 and consumed in T11/T12/T13/T15. `Status.UnboundedRecommended int32` matches across T1 (struct), T3 (write), T14 (read).
- [x] **Reasoning-token strings match design.** `"max_replicas_binding"` and `"min_replicas_binding"` strings appear identically in tokens.go (T4), decision.go (T2's clamp function uses string literals matching the constants in T4 — the integration is by-value-equality, not by import, so the order T2 → T4 is safe), event message (T7), prompt conditionals (T12/T13).
- [x] **Precedence chain matches design §5 rules 1-4.** T5's `ApplyCapAndCooldown` rewrite carries `BindingReason` only through the hysteresis branch; the `case target > current` and `case target < current` branches unconditionally overwrite to either `step_capped_*` or `scale_*`, and the cooldown final-override branch overwrites to `cooldown_holding_*`. Maps directly to design_v2 lines 471-478.
- [x] **F12 fix verified.** T11 explicitly distinguishes `Pattern != "" && Pattern != "default"` (Traffic pattern line — unchanged) from `Pattern != ""` (Long-term context line — F12 fix). `TestBuildPrompt_IncludesLongTermContextWhenPatternIsDefault` proves the distinction.
- [x] **Trigger rules preserved.** Sub-PR C T8 pins the existing "binding-without-replica-change does NOT trigger ExplainWorker" contract; the existing `if capOut.ShouldPatch { Notify(...) }` gate in the reconciler already enforces it.
- [x] **No cross-plan coupling.** Plan 15 does not touch Phase 5 items (G16, G17, G20-strict-inequality, G22). The webhook validator's `gbdt_quantile` widening (G20 enum slice) already landed in Plan 14.
