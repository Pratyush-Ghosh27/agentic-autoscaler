# Plan 16 — v2 Bug-fix Sweep Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix four independent bugs and cosmetic issues that block the v2 banner flip: (1) switch the re-classification trigger from `deploy.Generation` to the `deployment.kubernetes.io/revision` annotation so `/scale` patches stop causing spurious re-classifications (G16/F19); (2) seed the per-CR ring buffer with 5 copies on restart so the persisted `rpsPerPodCurrent` survives the first few observations (G17/F20); (3) tighten the webhook to reject `maxReplicas == minReplicas` (G20/F37); (4) migrate K8s Event `Reason` fields from snake_case to PascalCase per K8s convention (G22/F39).

**Architecture:** Strict TDD. Four independent sub-PRs — one per gap. Each sub-PR ships a failing test, the minimum code change to flip it green, a verification command, and a commit. No cross-sub-PR dependencies within this plan (G20's enum widening for `gbdt_quantile` already landed in Plan 14). The PascalCase migration (G22) touches every Event call-site so it lands last to minimize merge conflicts with any in-flight work on other branches.

**Tech Stack:** Go 1.22+, controller-runtime, kubebuilder markers, `testify`, envtest (for G16 only). No Python changes.

---

## Spec Coverage Map

| Plan item | Tasks | Source |
| --- | --- | --- |
| **G16** — Switch generation watcher to revision annotation | T1, T2, T3 | gap-report-v2.md G16, design_v2.md:768, F19 |
| **G17** — Ring-buffer 5-copy seed on restart | T4, T5 | gap-report-v2.md G17, design_v2.md F20 |
| **G20** — Webhook strict inequality (`maxReplicas <= minReplicas` rejected) | T6, T7 | gap-report-v2.md G20, design_v2.md:217 F37 |
| **G22** — K8s Event Reason PascalCase migration | T8, T9, T10, T11 | gap-report-v2.md G22, design_v2.md:491-511 F39 |

---

## File Structure

| Path | Sub-PR | Responsibility |
| --- | --- | --- |
| `internal/classifier/manager.go` | A | Change `ObserveDeploymentGeneration` to `ObserveDeploymentRevision`; track revision string instead of int64 generation. |
| `internal/classifier/manager_test.go` | A | Update tests to use revision annotation string. |
| `internal/controller/agenticautoscaler_controller.go` | A, D | Call `ObserveDeploymentRevision` instead of `ObserveDeploymentGeneration`; update Event call-sites to use PascalCase. |
| `internal/controller/agenticautoscaler_controller_test.go` | A, D | Envtest proving `/scale` does NOT fire re-classification; update event assertions for PascalCase. |
| `internal/decision/state.go` | B | Change `Seed` to accept a count parameter (or add `SeedN`). |
| `internal/decision/state_test.go` | B | Tests for 5-copy seed behaviour. |
| `internal/decision/decision.go` | B | Update `InitializeFromStatus` to seed 5 copies. |
| `internal/decision/decision_test.go` | B | Update existing `InitializeFromStatus` tests for 5 entries. |
| `internal/webhook/v1alpha1/validator.go` | C | Tighten `maxReplicas < minReplicas` to `maxReplicas <= minReplicas`. |
| `internal/webhook/v1alpha1/validator_test.go` | C | Update `TestValidateSpec_AcceptsMinEqualsMax` to assert rejection; add new acceptance test for `max > min`. |
| `internal/reasoning/tokens.go` | D | Add `PascalCase() string` method or a parallel `PascalReasons` map. |
| `internal/reasoning/tokens_test.go` | D | Pin the PascalCase mapping. |
| `internal/classifier/worker.go` | D | Use PascalCase for Event Reason field. |
| `internal/explainer/worker.go` | D | Use PascalCase for Event Reason field. |

---

## Sub-PR A: G16 — Switch generation watcher to revision annotation

The current code watches `deploy.Generation`, which is bumped by every `/scale` patch the controller itself issues. The spec (design_v2.md:768, F19) requires watching the `deployment.kubernetes.io/revision` annotation, which only changes on actual rollouts.

### Task 1: Rename and retype the Manager method + dedup field

**Files:**
- Modify: `internal/classifier/manager.go:42-47` (workerHandle struct), `:153-173` (ObserveDeploymentGeneration method)
- Test: `internal/classifier/manager_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/classifier/manager_test.go`, replace the four `ObserveDeploymentGeneration` tests with equivalent `ObserveDeploymentRevision` tests. Add a new file-level comment explaining the revision-based contract.

Replace `TestManager_ObserveDeploymentGeneration_FirstObservationDoesNotSignal` with:

```go
func TestManager_ObserveDeploymentRevision_FirstObservationDoesNotSignal(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	mgr, cancel := newManager(t, cr, prom)
	defer cancel()

	mgr.Ensure(cr)
	key := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}

	signalled := mgr.ObserveDeploymentRevision(key, "1")
	assert.False(t, signalled)
}
```

Replace `TestManager_ObserveDeploymentGeneration_SameGenerationDoesNotSignal` with:

```go
func TestManager_ObserveDeploymentRevision_SameRevisionDoesNotSignal(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	mgr, cancel := newManager(t, cr, prom)
	defer cancel()

	mgr.Ensure(cr)
	key := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}

	require.False(t, mgr.ObserveDeploymentRevision(key, "5"))
	assert.False(t, mgr.ObserveDeploymentRevision(key, "5"))
	assert.False(t, mgr.ObserveDeploymentRevision(key, "5"))
}
```

Replace `TestManager_ObserveDeploymentGeneration_ChangeSignalsOnce` with:

```go
func TestManager_ObserveDeploymentRevision_ChangeSignalsOnce(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	mgr, cancel := newManager(t, cr, prom)
	defer cancel()

	mgr.Ensure(cr)
	key := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}

	require.False(t, mgr.ObserveDeploymentRevision(key, "1"))
	assert.True(t, mgr.ObserveDeploymentRevision(key, "2"))
	assert.False(t, mgr.ObserveDeploymentRevision(key, "2"))
	assert.True(t, mgr.ObserveDeploymentRevision(key, "3"))
}
```

Replace `TestManager_ObserveDeploymentGeneration_UnknownKeyIsNoOp` with:

```go
func TestManager_ObserveDeploymentRevision_UnknownKeyIsNoOp(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	mgr, cancel := newManager(t, cr, prom)
	defer cancel()

	signalled := mgr.ObserveDeploymentRevision(
		types.NamespacedName{Namespace: "demo", Name: "absent"}, "7")
	assert.False(t, signalled)
}
```

Also add:

```go
func TestManager_ObserveDeploymentRevision_EmptyRevisionRecordsButDoesNotSignal(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	mgr, cancel := newManager(t, cr, prom)
	defer cancel()

	mgr.Ensure(cr)
	key := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}

	// Empty string is a valid first observation (annotation not yet set).
	signalled := mgr.ObserveDeploymentRevision(key, "")
	assert.False(t, signalled, "first observation — record only")

	// Non-empty after empty is a change.
	signalled = mgr.ObserveDeploymentRevision(key, "1")
	assert.True(t, signalled, "empty→non-empty is a revision change")
}
```

Update `TestManager_RootCtxCancelStopsAllWorkers` to call `ObserveDeploymentRevision` instead of `ObserveDeploymentGeneration`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp go test ./internal/classifier/... -run "ObserveDeploymentRevision" -v`
Expected: FAIL — `mgr.ObserveDeploymentRevision undefined`

- [ ] **Step 3: Implement the change**

In `internal/classifier/manager.go`:

1. Change `workerHandle` struct field from `lastDeploymentGen int64` to `lastDeploymentRevision string`:

```go
type workerHandle struct {
	cancel                 context.CancelFunc
	reclassifyCh           chan struct{}
	generationCh           chan struct{}
	lastDeploymentRevision string
	revisionInitialized    bool
}
```

2. Replace `ObserveDeploymentGeneration` method with `ObserveDeploymentRevision`:

```go
// ObserveDeploymentRevision tracks the target Deployment's
// `deployment.kubernetes.io/revision` annotation and pushes onto the
// worker's GenerationCh (drop-and-replace) when the value changes.
// Unlike metadata.generation, the revision annotation is only bumped on
// actual rollouts (image/env/command changes) — NOT on /scale patches.
// See design_v2.md:768 and F19.
//
// Returns true iff a revision-change signal was emitted.
func (m *Manager) ObserveDeploymentRevision(key types.NamespacedName, revision string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.workers[key]
	if !ok {
		return false
	}
	if !h.revisionInitialized {
		h.lastDeploymentRevision = revision
		h.revisionInitialized = true
		return false
	}
	if h.lastDeploymentRevision == revision {
		return false
	}
	h.lastDeploymentRevision = revision
	dropAndPush(h.generationCh)
	return true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp go test ./internal/classifier/... -v`
Expected: All PASS (including the updated `RootCtxCancel` test).

- [ ] **Step 5: Commit**

```bash
git add internal/classifier/manager.go internal/classifier/manager_test.go
git commit -m "refactor(classifier): switch ObserveDeploymentGeneration to ObserveDeploymentRevision (G16/F19)"
```

---

### Task 2: Wire the reconciler to read the revision annotation

**Files:**
- Modify: `internal/controller/agenticautoscaler_controller.go:189-192`

- [ ] **Step 1: Write the failing test (compile-time)**

No new test file needed — the reconciler already calls `r.Classifier.ObserveDeploymentGeneration(...)` which no longer exists after T1. The build itself is the test.

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp go build ./...`
Expected: FAIL — `r.Classifier.ObserveDeploymentGeneration undefined`

- [ ] **Step 2: Update the reconciler**

In `internal/controller/agenticautoscaler_controller.go`, replace the generation-change detection block (around line 189-192):

Old:
```go
	if r.Classifier != nil {
		r.Classifier.ObserveDeploymentGeneration(req.NamespacedName, deploy.Generation)
	}
```

New:
```go
	if r.Classifier != nil {
		revision := deploy.Annotations["deployment.kubernetes.io/revision"]
		r.Classifier.ObserveDeploymentRevision(req.NamespacedName, revision)
	}
```

- [ ] **Step 3: Verify build passes**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp go build ./...`
Expected: PASS

- [ ] **Step 4: Run full unit test suite**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp XDG_CACHE_HOME=/home/pratyush.ghosh/scaler/.cache go test ./internal/... -count=1`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/controller/agenticautoscaler_controller.go
git commit -m "feat(controller): read deployment.kubernetes.io/revision instead of .metadata.generation (G16/F19)"
```

---

### Task 3: Envtest proving /scale does NOT fire re-classification

**Files:**
- Modify: `internal/controller/agenticautoscaler_controller_test.go`

- [ ] **Step 1: Write the envtest**

Append a new `Describe` block to `internal/controller/agenticautoscaler_controller_test.go`:

```go
var _ = Describe("AgenticAutoscalerReconciler G16 revision watcher", func() {
	const ns = "rec-g16-revision"
	ctx := context.Background()

	BeforeEach(func() {
		ensureNamespace(ctx, ns)
	})

	It("does NOT signal re-classification when /scale patches bump metadata.generation", func() {
		const dep = "rev-deploy"
		const cr = "rev-cr"
		deploy := makeDeployment(ctx, ns, dep, 2)
		makeAAS(ctx, ns, cr, dep)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		// Spin up a real classifier manager so we can observe signals.
		workerCtx, cancelWorker := context.WithCancel(context.Background())
		DeferCleanup(cancelWorker)

		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(20, 500)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 1000, ModelUsed: "linear_extrap"}}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)

		mgr := classifier.NewManager(
			workerCtx,
			k8sClient,
			prom,
			&record.FakeRecorder{Events: make(chan string, 32)},
			classifier.WorkerConfig{
				Interval:       time.Hour,
				HistoryHours:   24 * time.Hour,
				MinPoints:      70,
				HighConfPoints: 240,
				DedupSeconds:   60,
			},
		)
		r.Classifier = mgr

		// First reconcile — seeds the revision observation.
		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		// Record the deploy's generation BEFORE and AFTER the reconcile-
		// driven /scale patch. The generation bumps, but the revision
		// annotation stays the same (no rollout happened).
		deployBefore := fetchDeploy(ctx, ns, dep)
		genBefore := deployBefore.Generation
		revBefore := deployBefore.Annotations["deployment.kubernetes.io/revision"]

		// Second reconcile — the /scale patch from the first reconcile
		// bumped generation; this reconcile sees the new generation.
		_, err = reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		deployAfter := fetchDeploy(ctx, ns, dep)
		genAfter := deployAfter.Generation
		revAfter := deployAfter.Annotations["deployment.kubernetes.io/revision"]

		// Generation SHOULD have changed (envtest bumps it on spec write).
		// If it didn't bump, the test premise is invalid — skip.
		if genBefore == genAfter {
			Skip("envtest did not bump generation on /scale; test premise invalid")
		}

		// Revision must NOT have changed — /scale is not a rollout.
		Expect(revAfter).To(Equal(revBefore),
			"deployment.kubernetes.io/revision must not change on /scale patches")

		// The classifier manager must NOT have received a generation signal.
		// We can't directly observe the channel, but we can verify the
		// reconciler read the revision (unchanged) and did not signal.
		// The indirect proof: if it HAD signalled, the worker's dedup
		// window would have fired and we'd see a pattern_classified event.
		// With a 1-hour interval and no signal, no classification runs.
		Consistently(func() *autoscalingv1alpha1.ClassifiedParams {
			return fetch(ctx, ns, cr).Status.ClassifiedParams
		}, "500ms", "50ms").Should(BeNil(),
			"no re-classification should fire from a /scale patch — "+
				"revision annotation unchanged")
	})

	It("signals re-classification when the revision annotation changes (simulated rollout)", func() {
		const dep = "rev-rollout-deploy"
		const cr = "rev-rollout-cr"
		deploy := makeDeployment(ctx, ns, dep, 2)
		makeAAS(ctx, ns, cr, dep)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &autoscalingv1alpha1.AgenticAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cr}})
			_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: dep}})
		})

		prom := &fakePromQuerier{instantVal: 500, rangeVal: rangeSamples(80, 500)}
		fc := &fakeForecaster{resp: forecast.RecommendResponse{PredictedRPS: 600, ModelUsed: "linear_extrap"}}
		ex := &fakeExplainNotifier{}
		r := newReconciler(prom, fc, ex)

		workerCtx, cancelWorker := context.WithCancel(context.Background())
		DeferCleanup(cancelWorker)

		mgr := classifier.NewManager(
			workerCtx,
			k8sClient,
			prom,
			&record.FakeRecorder{Events: make(chan string, 32)},
			classifier.WorkerConfig{
				Interval:       time.Hour,
				HistoryHours:   24 * time.Hour,
				MinPoints:      70,
				HighConfPoints: 240,
				DedupSeconds:   1,
			},
		)
		r.Classifier = mgr

		// First reconcile seeds the revision.
		_, err := reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		// Simulate a rollout by setting the revision annotation.
		deploy = fetchDeploy(ctx, ns, dep)
		if deploy.Annotations == nil {
			deploy.Annotations = map[string]string{}
		}
		deploy.Annotations["deployment.kubernetes.io/revision"] = "2"
		Expect(k8sClient.Update(ctx, deploy)).To(Succeed())

		// Second reconcile reads the new revision and signals.
		_, err = reconcileFor(ctx, r, ns, cr)
		Expect(err).NotTo(HaveOccurred())

		// The classifier worker should eventually run and produce
		// classifiedParams (since prom has 80 samples >= MinPoints=70).
		Eventually(func() *autoscalingv1alpha1.ClassifiedParams {
			return fetch(ctx, ns, cr).Status.ClassifiedParams
		}, "10s", "100ms").ShouldNot(BeNil(),
			"revision change must trigger re-classification")
	})
})
```

- [ ] **Step 2: Run the envtest**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp XDG_CACHE_HOME=/home/pratyush.ghosh/scaler/.cache KUBEBUILDER_ASSETS=$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@latest use -p path) go test ./internal/controller/... -run "G16" -v -timeout 120s`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/controller/agenticautoscaler_controller_test.go
git commit -m "test(envtest): prove /scale does NOT fire re-classification; revision change does (G16/F19)"
```

---

## Sub-PR B: G17 — Ring-buffer 5-copy seed on restart

### Task 4: Change Seed to push N copies

**Files:**
- Modify: `internal/decision/state.go:47-49`
- Test: `internal/decision/state_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/decision/state_test.go`, add:

```go
func TestRingBuffer_SeedN_PushesFiveCopies(t *testing.T) {
	rb := decision.NewRingBuffer(10)
	rb.Push(10)
	rb.Push(20)
	rb.SeedN(175, 5)
	require.Len(t, rb.Values(), 5)
	for _, v := range rb.Values() {
		assert.InDelta(t, 175.0, v, 0.001)
	}
	assert.InDelta(t, 175.0, rb.Median(), 0.001)
}

func TestRingBuffer_SeedN_ClampsToCap(t *testing.T) {
	rb := decision.NewRingBuffer(3)
	rb.SeedN(99, 5)
	// Only 3 fit in the buffer.
	require.Len(t, rb.Values(), 3)
	assert.InDelta(t, 99.0, rb.Median(), 0.001)
}

func TestRingBuffer_SeedN_ZeroCountClearsBuffer(t *testing.T) {
	rb := decision.NewRingBuffer(10)
	rb.Push(42)
	rb.SeedN(100, 0)
	assert.Empty(t, rb.Values())
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp go test ./internal/decision/... -run "SeedN" -v`
Expected: FAIL — `rb.SeedN undefined`

- [ ] **Step 3: Implement SeedN**

In `internal/decision/state.go`, add the new method and update the existing `Seed` to call it:

```go
// SeedN replaces the buffer's contents with n copies of v. Used during
// restart recovery to preserve the persisted rps_per_pod estimate across
// the next n observations (design_v2.md F20: 5-copy seed). If n exceeds
// the buffer capacity, only cap copies are stored.
func (rb *RingBuffer) SeedN(v float64, n int) {
	rb.data = rb.data[:0]
	count := n
	if count > rb.cap {
		count = rb.cap
	}
	for i := 0; i < count; i++ {
		rb.data = append(rb.data, v)
	}
}

// Seed replaces the buffer's contents with a single observation.
// Deprecated: prefer SeedN for restart recovery (5-copy seed per F20).
func (rb *RingBuffer) Seed(v float64) {
	rb.SeedN(v, 1)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp go test ./internal/decision/... -v`
Expected: All PASS (including existing `TestRingBuffer_SeedReplacesContents` which still seeds 1 copy).

- [ ] **Step 5: Commit**

```bash
git add internal/decision/state.go internal/decision/state_test.go
git commit -m "feat(decision): add SeedN for multi-copy ring-buffer seeding (G17/F20)"
```

---

### Task 5: Update InitializeFromStatus to seed 5 copies

**Files:**
- Modify: `internal/decision/decision.go:332-334`
- Test: `internal/decision/decision_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/decision/decision_test.go`, update `TestInitializeState_FromStatus`:

```go
func TestInitializeState_FromStatus(t *testing.T) {
	state := &decision.PerCRState{Observations: decision.NewRingBuffer(10)}
	lastScale := time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC)
	decision.InitializeFromStatus(state, decision.StatusSeed{
		RpsPerPodCurrent: 175,
		InBounds:         true,
		LastScaleTime:    lastScale,
	})
	assert.True(t, state.Initialized)
	assert.InDelta(t, 175.0, state.RpsPerPod, 0.001)
	assert.Len(t, state.Observations.Values(), 5,
		"F20: restart recovery seeds 5 copies to preserve the estimate")
	assert.Equal(t, lastScale, state.LastScaleUpTime)
	assert.Equal(t, lastScale, state.LastScaleDownTime)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp go test ./internal/decision/... -run TestInitializeState_FromStatus -v`
Expected: FAIL — `assert.Len` expected 5, got 1.

- [ ] **Step 3: Update InitializeFromStatus**

In `internal/decision/decision.go`, change the seed call from:

```go
	if seed.InBounds && seed.RpsPerPodCurrent > 0 {
		state.RpsPerPod = seed.RpsPerPodCurrent
		state.Observations.Seed(seed.RpsPerPodCurrent)
```

To:

```go
	if seed.InBounds && seed.RpsPerPodCurrent > 0 {
		state.RpsPerPod = seed.RpsPerPodCurrent
		state.Observations.SeedN(seed.RpsPerPodCurrent, 5)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp go test ./internal/decision/... -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/decision/decision.go internal/decision/decision_test.go
git commit -m "feat(decision): seed ring buffer with 5 copies on restart recovery (G17/F20)"
```

---

## Sub-PR C: G20 — Webhook strict inequality

### Task 6: Tighten the webhook to reject maxReplicas == minReplicas

**Files:**
- Modify: `internal/webhook/v1alpha1/validator.go:36-41`
- Test: `internal/webhook/v1alpha1/validator_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/webhook/v1alpha1/validator_test.go`, change `TestValidateSpec_AcceptsMinEqualsMax` to assert rejection:

```go
func TestValidateSpec_RejectsMinEqualsMax(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(5)
	cr.Spec.MaxReplicas = ptr32(5)

	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.Error(t, err, "min == max must be rejected per F37")
	assert.Contains(t, err.Error(), "maxReplicas")
	assert.Contains(t, err.Error(), "minReplicas")
}
```

Also add a test to confirm the boundary is correct:

```go
func TestValidateSpec_AcceptsMaxOneAboveMin(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(5)
	cr.Spec.MaxReplicas = ptr32(6)

	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.NoError(t, err, "max = min+1 is the smallest valid range")
}
```

- [ ] **Step 2: Run tests to verify the rejection test fails**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp go test ./internal/webhook/... -run "RejectsMinEqualsMax" -v`
Expected: FAIL — the current code allows `min == max`.

- [ ] **Step 3: Tighten the validator**

In `internal/webhook/v1alpha1/validator.go`, change the replica-bounds check from:

```go
	if spec.MinReplicas != nil && spec.MaxReplicas != nil &&
		*spec.MaxReplicas < *spec.MinReplicas {
		problems = append(problems, fmt.Sprintf(
			"maxReplicas=%d must be >= minReplicas=%d",
			*spec.MaxReplicas, *spec.MinReplicas))
	}
```

To:

```go
	if spec.MinReplicas != nil && spec.MaxReplicas != nil &&
		*spec.MaxReplicas <= *spec.MinReplicas {
		problems = append(problems, fmt.Sprintf(
			"maxReplicas=%d must be > minReplicas=%d",
			*spec.MaxReplicas, *spec.MinReplicas))
	}
```

- [ ] **Step 4: Run all webhook tests**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp go test ./internal/webhook/... -v`
Expected: All PASS (the old `AcceptsMinEqualsMax` test is gone; `RejectsMinEqualsMax` passes; `AcceptsMaxOneAboveMin` passes; `RejectsMaxStepSizeAboveRange` still works because its range is `5 - 2 = 3`).

- [ ] **Step 5: Commit**

```bash
git add internal/webhook/v1alpha1/validator.go internal/webhook/v1alpha1/validator_test.go
git commit -m "fix(webhook): reject maxReplicas == minReplicas per F37 strict inequality (G20)"
```

---

### Task 7: Update maxStepSize validation for the strict inequality

**Files:**
- Modify: `internal/webhook/v1alpha1/validator.go:61-68`
- Test: `internal/webhook/v1alpha1/validator_test.go`

- [ ] **Step 1: Write the failing test**

The `maxStepSize` check uses `rangeSize := *spec.MaxReplicas - *spec.MinReplicas`. With `min == max` now rejected, the smallest valid range is `max - min = 1`. But there's a new edge case: `maxStepSize` must also be valid when `rangeSize == 1`. Existing tests cover `maxStepSize = 3` with range `5-2=3` (at boundary). Add:

```go
func TestValidateSpec_AcceptsMaxStepSizeOneWithMinimalRange(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(5)
	cr.Spec.MaxReplicas = ptr32(6)
	cr.Spec.MaxStepSize = ptr32(1)
	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.NoError(t, err)
}

func TestValidateSpec_RejectsMaxStepSizeTwoWithMinimalRange(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(5)
	cr.Spec.MaxReplicas = ptr32(6)
	cr.Spec.MaxStepSize = ptr32(2)
	err := webhookv1alpha1.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maxStepSize")
}
```

- [ ] **Step 2: Run tests to verify they pass (no code change needed)**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp go test ./internal/webhook/... -run "MaxStepSize.*MinimalRange" -v`
Expected: PASS — the existing `rangeSize` calculation already handles this correctly. These tests just pin the new boundary.

- [ ] **Step 3: Commit**

```bash
git add internal/webhook/v1alpha1/validator_test.go
git commit -m "test(webhook): pin maxStepSize edge cases with minimal range (G20/F37)"
```

---

## Sub-PR D: G22 — K8s Event Reason PascalCase migration

### Task 8: Add PascalCase mapping to reasoning package

**Files:**
- Modify: `internal/reasoning/tokens.go`
- Test: `internal/reasoning/tokens_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/reasoning/tokens_test.go`, add:

```go
func TestPascalReason_AllTokensHaveMapping(t *testing.T) {
	for name, snake := range AllTokens() {
		pascal := PascalReason(snake)
		assert.NotEmpty(t, pascal, "token %s (%s) has no PascalCase mapping", name, snake)
		// PascalCase must start with uppercase.
		assert.Regexp(t, `^[A-Z]`, pascal, "PascalReason(%q) must be PascalCase", snake)
	}
}

func TestPascalReason_SpecificMappings(t *testing.T) {
	cases := []struct{ snake, pascal string }{
		{"scale_up", "ScaleUp"},
		{"scale_down", "ScaleDown"},
		{"no_change", "NoChange"},
		{"step_capped_up", "StepCappedUp"},
		{"step_capped_down", "StepCappedDown"},
		{"cooldown_holding_up", "CooldownHoldingUp"},
		{"cooldown_holding_down", "CooldownHoldingDown"},
		{"max_replicas_binding", "MaxReplicasBinding"},
		{"min_replicas_binding", "MinReplicasBinding"},
		{"kill_switched", "KillSwitched"},
		{"conflict_detected", "ConflictDetected"},
		{"forecast_unavailable", "ForecastUnavailable"},
		{"metrics_unavailable", "MetricsUnavailable"},
		{"pattern_classified", "PatternClassified"},
		{"pattern_unknown", "PatternUnknown"},
		{"scale_explained", "ScaleExplained"},
	}
	for _, tc := range cases {
		t.Run(tc.snake, func(t *testing.T) {
			assert.Equal(t, tc.pascal, PascalReason(tc.snake))
		})
	}
}

func TestPascalReason_UnknownTokenReturnsSelf(t *testing.T) {
	assert.Equal(t, "unknown_token", PascalReason("unknown_token"),
		"unmapped tokens pass through unchanged for safety")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp go test ./internal/reasoning/... -run "PascalReason" -v`
Expected: FAIL — `PascalReason undefined`

- [ ] **Step 3: Implement PascalReason**

In `internal/reasoning/tokens.go`, add after the `AllTokens()` function:

```go
// pascalMap maps snake_case reasoning tokens to their PascalCase K8s Event
// Reason equivalents. See design_v2.md:491-511 for the canonical table.
var pascalMap = map[string]string{
	ScaleUp:             "ScaleUp",
	ScaleDown:           "ScaleDown",
	NoChange:            "NoChange",
	StepCappedUp:        "StepCappedUp",
	StepCappedDown:      "StepCappedDown",
	CooldownHoldingUp:   "CooldownHoldingUp",
	CooldownHoldingDown: "CooldownHoldingDown",
	MaxReplicasBinding:  "MaxReplicasBinding",
	MinReplicasBinding:  "MinReplicasBinding",
	KillSwitched:        "KillSwitched",
	ConflictDetected:    "ConflictDetected",
	ForecastUnavailable: "ForecastUnavailable",
	MetricsUnavailable:  "MetricsUnavailable",
	PatternClassified:   "PatternClassified",
	PatternUnknown:      "PatternUnknown",
	ScaleExplained:      "ScaleExplained",
}

// PascalReason returns the PascalCase K8s Event Reason for the given
// snake_case reasoning token. Returns the input unchanged if no mapping
// exists (defensive — all known tokens are mapped).
func PascalReason(snake string) string {
	if p, ok := pascalMap[snake]; ok {
		return p
	}
	return snake
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp go test ./internal/reasoning/... -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/reasoning/tokens.go internal/reasoning/tokens_test.go
git commit -m "feat(reasoning): add PascalReason mapping for K8s Event Reason field (G22/F39)"
```

---

### Task 9: Migrate reconciler Event Reason to PascalCase

**Files:**
- Modify: `internal/controller/agenticautoscaler_controller.go` (lines 137, 147, 153, 176, 267, 273, 342, 377)

- [ ] **Step 1: Write the failing test**

In `internal/controller/agenticautoscaler_controller_test.go`, the existing G13 tests assert `ContainSubstring(reasoning.MaxReplicasBinding)` which matches both the Reason field and the message body. After migration, the Reason is PascalCase but the message body still contains the snake_case token. Update the first G13 test's event assertion to assert PascalCase in the Reason position:

```go
// In the "persists UnboundedRecommended..." test, change the event assertion to:
Eventually(fakeRec.Events).Should(Receive(
	SatisfyAll(
		ContainSubstring("MaxReplicasBinding"),
		ContainSubstring("max_replicas_binding"),
		ContainSubstring("unboundedRecommended=80"),
	)))
```

This test will fail because the current code uses snake_case as the Reason.

- [ ] **Step 2: Run the test to verify it fails**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp XDG_CACHE_HOME=/home/pratyush.ghosh/scaler/.cache go test ./internal/controller/... -run "G13.*persists" -v -timeout 120s`
Expected: FAIL — event reason is `max_replicas_binding` not `MaxReplicasBinding`.

Note: The FakeRecorder formats events as `<EventType> <Reason> <Message>`. After the migration, Reason will be PascalCase and the message body will contain the snake_case token.

- [ ] **Step 3: Migrate the reconciler**

In `internal/controller/agenticautoscaler_controller.go`, make these changes:

1. In the Event call at line 137 (metrics unavailable):
   Change: `reasoning.MetricsUnavailable` → `reasoning.PascalReason(reasoning.MetricsUnavailable)`

2. In the Event call at line 147 (metrics unavailable):
   Same change.

3. In the Event call at line 153 (metrics unavailable):
   Same change.

4. In the Event call at line 176 (forecast unavailable):
   Change: `reasoning.ForecastUnavailable` → `reasoning.PascalReason(reasoning.ForecastUnavailable)`

5. In the Eventf calls at lines 267 and 273 (the capOut.Reason event):
   Change: `capOut.Reason` → `reasoning.PascalReason(capOut.Reason)`

   Also include the snake_case token in the message body. Update the format strings:

   Line 267-271 (unbounded != recommended case):
   ```go
   r.EventRecorder.Eventf(&aas, corev1.EventTypeNormal, reasoning.PascalReason(capOut.Reason),
       "%s current_rps=%.1f predicted_rps=%.1f current=%d target=%d "+
           "recommended=%d unboundedRecommended=%d model=%s",
       capOut.Reason, currentRPS, forecastResp.PredictedRPS, currentReplicas, capOut.Target,
       recommended, unbounded, forecastResp.ModelUsed)
   ```

   Line 273-275 (common case):
   ```go
   r.EventRecorder.Eventf(&aas, corev1.EventTypeNormal, reasoning.PascalReason(capOut.Reason),
       "%s current_rps=%.1f predicted_rps=%.1f current=%d target=%d model=%s",
       capOut.Reason, currentRPS, forecastResp.PredictedRPS, currentReplicas, capOut.Target, forecastResp.ModelUsed)
   ```

6. In the Event call at line 342 (kill switch):
   Change: `reasoning.KillSwitched` → `reasoning.PascalReason(reasoning.KillSwitched)`

7. In the Event call at line 377 (conflict detected):
   Change: `reasoning.ConflictDetected` → `reasoning.PascalReason(reasoning.ConflictDetected)`

- [ ] **Step 4: Run all controller tests**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp XDG_CACHE_HOME=/home/pratyush.ghosh/scaler/.cache go test ./internal/controller/... -v -timeout 120s`
Expected: PASS (including G13 tests with updated assertions and pre-checks tests).

- [ ] **Step 5: Commit**

```bash
git add internal/controller/agenticautoscaler_controller.go \
        internal/controller/agenticautoscaler_controller_test.go
git commit -m "feat(controller): emit PascalCase Event Reason with snake_case in body (G22/F39)"
```

---

### Task 10: Migrate classifier worker Event Reason to PascalCase

**Files:**
- Modify: `internal/classifier/worker.go:364-372`

- [ ] **Step 1: Write the failing test**

In `internal/classifier/worker_test.go`, the existing tests check events via `contains(e, reasoning.PatternClassified)`. After migration, the event string will contain `PatternClassified` (PascalCase) as the Reason and `pattern_classified` in the message body. Update the assertion helper or the individual assertions.

Find the relevant assertion in the test (around line 189):
```go
return contains(e, reasoning.PatternClassified)
```

Change to assert PascalCase:
```go
return contains(e, "PatternClassified")
```

And similarly for `PatternUnknown` at line 219:
```go
return contains(e, "PatternUnknown")
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp go test ./internal/classifier/... -run "Worker" -v`
Expected: FAIL — events still contain snake_case reason.

- [ ] **Step 3: Update the classifier worker**

In `internal/classifier/worker.go`, change the `emitEvent` method at line 364-372:

```go
func (w *Worker) emitEvent(ctx context.Context, reason, message string) {
	if w.EventRecorder == nil {
		return
	}
	var aas autoscalingv1alpha1.AgenticAutoscaler
	if err := w.Client.Get(ctx, w.Key, &aas); err != nil {
		return
	}
	w.EventRecorder.Event(&aas, corev1.EventTypeNormal, reasoning.PascalReason(reason),
		reason+" "+message)
}
```

Add the import for `reasoning` if not already present (it should be — the file already uses `reasoning.PatternClassified` etc.).

- [ ] **Step 4: Run tests to verify they pass**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp go test ./internal/classifier/... -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/classifier/worker.go internal/classifier/worker_test.go
git commit -m "feat(classifier): emit PascalCase Event Reason with snake_case in body (G22/F39)"
```

---

### Task 11: Migrate explainer worker Event Reason to PascalCase

**Files:**
- Modify: `internal/explainer/worker.go:153`

- [ ] **Step 1: Write the failing test**

The explainer worker currently emits:
```go
w.EventRecorder.Event(&aas, corev1.EventTypeNormal, reasoning.ScaleExplained, message)
```

In `internal/explainer/worker_test.go`, find the test that asserts the event contains `scale_explained` and update it to expect `ScaleExplained` as the Reason. If no such explicit test exists, add one:

```go
func TestWorker_EmitsScaleExplainedEvent_PascalCaseReason(t *testing.T) {
	// This test will be wired into the existing worker_test.go structure.
	// The key assertion is that the FakeRecorder receives an event with
	// "ScaleExplained" as the Reason (PascalCase per G22/F39).
	// The message body must contain the LLM-generated explanation.
}
```

For now, the compile-time change is minimal. The existing worker tests that check for `reasoning.ScaleExplained` in the event string will fail once we change the Reason to PascalCase.

- [ ] **Step 2: Update the explainer worker**

In `internal/explainer/worker.go`, change line 153 from:

```go
w.EventRecorder.Event(&aas, corev1.EventTypeNormal, reasoning.ScaleExplained, message)
```

To:

```go
w.EventRecorder.Event(&aas, corev1.EventTypeNormal, reasoning.PascalReason(reasoning.ScaleExplained),
    reasoning.ScaleExplained+" "+message)
```

Add the `reasoning` import if not already present (check first — `internal/explainer/worker.go` may already import it).

- [ ] **Step 3: Run all tests**

Run: `TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp go test ./internal/explainer/... -v`
Expected: PASS

- [ ] **Step 4: Run the full pre-flight**

Run: `make pre-flight`
Expected: OK — all lint, codegen, Go tests, Python tests, and envtests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/explainer/worker.go
git commit -m "feat(explainer): emit PascalCase Event Reason with snake_case in body (G22/F39)"
```

---

## Final Verification

- [ ] **Step 1: Run `make pre-flight`**

```bash
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
XDG_CACHE_HOME=/home/pratyush.ghosh/scaler/.cache \
GOLANGCI_LINT_CACHE=/home/pratyush.ghosh/scaler/.cache/golangci \
make pre-flight
```

Expected: `OK`

- [ ] **Step 2: Push and create PR**

```bash
git push -u origin HEAD:refs/for/main
```

Or create a feature branch first:
```bash
git checkout -b feat/v2-bugfix-sweep
git push -u origin feat/v2-bugfix-sweep
```

---

## Post-merge spec trailer

Once this plan is merged, land **E2** (condense the PascalCase mapping table in design_v2.md to a one-liner). This is a separate docs-only commit:

> *"K8s Event `Reason` field uses PascalCase (e.g., `ScaleUp`, `MaxReplicasBinding`); the snake_case token is included verbatim in the message body for log searchability."*

This replaces the 16-row table at design_v2.md:493-511.
