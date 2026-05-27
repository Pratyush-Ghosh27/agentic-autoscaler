/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package classifier

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	autoscalingv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/api/v1alpha1"
)

// Manager owns the lifecycle of one ClassifierWorker per
// AgenticAutoscaler CR. The reconciler calls Ensure on every reconcile
// (idempotent) and Stop on delete. The three trigger sources from
// docs/design.md §6.1 are exposed as separate signal methods so the
// reconciler doesn't need to know about the worker's channel internals.
//
// All public methods are safe to call from multiple goroutines.
type Manager struct {
	rootCtx       context.Context
	client        client.Client
	prom          PromQuerier
	eventRecorder record.EventRecorder
	config        WorkerConfig

	mu      sync.Mutex
	workers map[types.NamespacedName]*workerHandle
}

type workerHandle struct {
	cancel       context.CancelFunc
	reclassifyCh chan struct{}
	generationCh chan struct{}
	// lastDeploymentRevision tracks the most recent value of the target
	// Deployment's `deployment.kubernetes.io/revision` annotation observed
	// via ObserveDeploymentRevision. Unlike metadata.generation, the
	// revision annotation is only bumped on actual rollouts (image / env /
	// command changes that produce a new ReplicaSet) — NOT on /scale
	// patches. See design_v2.md:768 and F19.
	lastDeploymentRevision string
	revisionInitialized    bool
}

// NewManager constructs a Manager. rootCtx is the long-lived context
// (typically `mgr.Start()`'s ctx) that bounds every worker's lifetime;
// individual workers are torn down by Stop or when rootCtx is cancelled.
func NewManager(
	rootCtx context.Context,
	c client.Client,
	prom PromQuerier,
	rec record.EventRecorder,
	cfg WorkerConfig,
) *Manager {
	return &Manager{
		rootCtx:       rootCtx,
		client:        c,
		prom:          prom,
		eventRecorder: rec,
		config:        cfg,
		workers:       map[types.NamespacedName]*workerHandle{},
	}
}

// Ensure starts a worker for cr if none is already running. Safe to call
// from every reconcile — repeated calls for the same CR are no-ops.
//
// MinReplicas / MaxReplicas are read from the CR's spec at start time.
// If the CR's bounds change later, Stop+Ensure (e.g. via a delete and
// re-create) is the supported path; piecemeal in-flight updates aren't.
func (m *Manager) Ensure(cr *autoscalingv1alpha1.AgenticAutoscaler) {
	key := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.workers[key]; ok {
		return
	}

	minRep := int32(2)
	if cr.Spec.MinReplicas != nil {
		minRep = *cr.Spec.MinReplicas
	}
	maxRep := int32(10)
	if cr.Spec.MaxReplicas != nil {
		maxRep = *cr.Spec.MaxReplicas
	}

	reclassify := make(chan struct{}, 1)
	generation := make(chan struct{}, 1)
	workerCtx, cancel := context.WithCancel(m.rootCtx)

	w := &Worker{
		Key:            key,
		DeploymentName: cr.Spec.TargetRef.Name,
		MinReplicas:    minRep,
		MaxReplicas:    maxRep,
		Config:         m.config,
		Client:         m.client,
		Prom:           m.prom,
		EventRecorder:  m.eventRecorder,
		ReclassifyCh:   reclassify,
		GenerationCh:   generation,
	}

	m.workers[key] = &workerHandle{
		cancel:       cancel,
		reclassifyCh: reclassify,
		generationCh: generation,
	}

	go w.Run(workerCtx)
}

// Stop cancels and removes the worker for key. Safe to call multiple
// times; calling Stop for a key with no running worker is a no-op.
func (m *Manager) Stop(key types.NamespacedName) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.workers[key]
	if !ok {
		return
	}
	h.cancel()
	delete(m.workers, key)
}

// SignalReclassify pushes a reclassify trigger onto the worker's
// channel using drop-and-replace semantics (design §6.1 trigger 3).
// No-op if no worker exists for key.
func (m *Manager) SignalReclassify(key types.NamespacedName) {
	m.mu.Lock()
	h, ok := m.workers[key]
	m.mu.Unlock()
	if !ok {
		return
	}
	dropAndPush(h.reclassifyCh)
}

// ObserveDeploymentRevision tracks the target Deployment's
// `deployment.kubernetes.io/revision` annotation and pushes onto the
// worker's GenerationCh (drop-and-replace) when the value changes.
// Unlike metadata.generation, the revision annotation is only bumped on
// actual rollouts (image / env / command changes that produce a new
// ReplicaSet) — NOT on /scale patches the controller itself issues.
// See design_v2.md:768 and F19.
//
// The worker's own dedup window (CLASSIFIER_DEDUP_SECONDS) suppresses
// runaway reclassification when the CR's first reconcile and the
// informer's first sync both observe a "new" revision simultaneously.
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
		// First observation — record but don't fire. The worker's
		// immediate-first-run trigger has already classified at
		// startup; signalling here would just queue a redundant pass.
		// We use revisionInitialized rather than `revision == ""` as
		// the first-observation marker because empty is a legitimate
		// initial state (no rollout has happened yet on this Deployment).
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

// LastDeploymentRevision returns the revision string most recently
// observed via ObserveDeploymentRevision for the given key. Returns
// the empty string when no observation has been recorded (worker not
// running, or key unknown). Exposed for tests that need to verify the
// reconciler is reading the revision annotation rather than the
// generation field.
func (m *Manager) LastDeploymentRevision(key types.NamespacedName) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.workers[key]
	if !ok {
		return ""
	}
	return h.lastDeploymentRevision
}

// HasWorker reports whether a worker is currently running for key.
// Exposed for tests and operational tooling — production code should
// not branch on this.
func (m *Manager) HasWorker(key types.NamespacedName) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.workers[key]
	return ok
}

// dropAndPush implements the drop-and-replace pattern documented in
// design §6.2: send if the channel is empty; otherwise drain the stale
// value and send the new one. Never blocks.
func dropAndPush(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}
