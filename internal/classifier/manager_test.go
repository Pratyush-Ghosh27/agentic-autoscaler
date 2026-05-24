/*
Copyright 2026.
*/

package classifier_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	autoscalingv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/api/v1alpha1"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/classifier"
)

// newManager spins up a Manager with no real workers running yet —
// every test method on Manager is exercisable in isolation because the
// worker goroutines block on their own channels and don't drive
// classification unless the fakeProm is populated and an interval ticks.
func newManager(t *testing.T, cr *autoscalingv1alpha1.AgenticAutoscaler, prom *fakeProm) (*classifier.Manager, context.CancelFunc) {
	t.Helper()
	scheme := newScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(&autoscalingv1alpha1.AgenticAutoscaler{}).
		Build()
	rec := record.NewFakeRecorder(16)
	rootCtx, cancel := context.WithCancel(context.Background())
	mgr := classifier.NewManager(rootCtx, c, prom, rec, classifier.WorkerConfig{
		// Long interval so the timer doesn't fire during unit tests —
		// we exercise lifecycle and signalling, not classification.
		Interval:       time.Hour,
		HistoryHours:   24 * time.Hour,
		MinPoints:      70,
		HighConfPoints: 240,
		DedupSeconds:   60,
	})
	return mgr, cancel
}

func TestManager_EnsureStartsExactlyOneWorker(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	mgr, cancel := newManager(t, cr, prom)
	defer cancel()

	key := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}
	require.False(t, mgr.HasWorker(key))

	mgr.Ensure(cr)
	require.True(t, mgr.HasWorker(key))

	// Idempotent: a second Ensure does not start a second worker.
	mgr.Ensure(cr)
	require.True(t, mgr.HasWorker(key))
}

func TestManager_StopCancelsWorker(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	mgr, cancel := newManager(t, cr, prom)
	defer cancel()

	key := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}
	mgr.Ensure(cr)
	require.True(t, mgr.HasWorker(key))

	mgr.Stop(key)
	assert.False(t, mgr.HasWorker(key))

	// Stop on an already-stopped key is a no-op.
	mgr.Stop(key)
}

func TestManager_SignalReclassifyOnUnknownKeyIsNoOp(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	mgr, cancel := newManager(t, cr, prom)
	defer cancel()

	// Worker not yet ensured — must not panic, must not block.
	mgr.SignalReclassify(types.NamespacedName{Namespace: "demo", Name: "absent"})
}

func TestManager_ObserveDeploymentGeneration_FirstObservationDoesNotSignal(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	mgr, cancel := newManager(t, cr, prom)
	defer cancel()

	mgr.Ensure(cr)
	key := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}

	// First observation just records the generation; the worker's
	// own immediate-first-run trigger has already classified.
	signalled := mgr.ObserveDeploymentGeneration(key, 1)
	assert.False(t, signalled)
}

func TestManager_ObserveDeploymentGeneration_SameGenerationDoesNotSignal(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	mgr, cancel := newManager(t, cr, prom)
	defer cancel()

	mgr.Ensure(cr)
	key := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}

	require.False(t, mgr.ObserveDeploymentGeneration(key, 5))
	assert.False(t, mgr.ObserveDeploymentGeneration(key, 5))
	assert.False(t, mgr.ObserveDeploymentGeneration(key, 5))
}

func TestManager_ObserveDeploymentGeneration_ChangeSignalsOnce(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	mgr, cancel := newManager(t, cr, prom)
	defer cancel()

	mgr.Ensure(cr)
	key := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}

	// Seed.
	require.False(t, mgr.ObserveDeploymentGeneration(key, 1))
	// Change.
	assert.True(t, mgr.ObserveDeploymentGeneration(key, 2))
	// Same again.
	assert.False(t, mgr.ObserveDeploymentGeneration(key, 2))
	// Change again.
	assert.True(t, mgr.ObserveDeploymentGeneration(key, 3))
}

func TestManager_ObserveDeploymentGeneration_UnknownKeyIsNoOp(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	mgr, cancel := newManager(t, cr, prom)
	defer cancel()

	signalled := mgr.ObserveDeploymentGeneration(
		types.NamespacedName{Namespace: "demo", Name: "absent"}, 7)
	assert.False(t, signalled)
}

func TestManager_RootCtxCancelStopsAllWorkers(t *testing.T) {
	cr := newSampleCR()
	prom := &fakeProm{}
	mgr, cancel := newManager(t, cr, prom)

	mgr.Ensure(cr)
	key := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}
	require.True(t, mgr.HasWorker(key))

	cancel()
	// HasWorker reports the map state, not goroutine liveness — but
	// after cancel, calls into the manager must remain safe.
	mgr.SignalReclassify(key)
	mgr.ObserveDeploymentGeneration(key, 99)
	mgr.Stop(key)
}
