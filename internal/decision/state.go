/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package decision

import (
	"sort"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// RingBuffer holds up to `cap` float64 observations in FIFO order. Used to
// smooth `rps_per_pod` observations across reconcile cycles per design §5.
//
// RingBuffer is NOT goroutine-safe — callers (PerCRState) own a single
// instance and must serialise access externally. The StateStore wrapping
// PerCRState provides that guarantee.
type RingBuffer struct {
	data []float64
	cap  int
}

// NewRingBuffer returns a ring buffer with the given capacity.
func NewRingBuffer(capacity int) *RingBuffer {
	return &RingBuffer{data: make([]float64, 0, capacity), cap: capacity}
}

// Push appends v, evicting the oldest entry once the buffer is full.
func (rb *RingBuffer) Push(v float64) {
	if len(rb.data) == rb.cap {
		rb.data = rb.data[1:]
	}
	rb.data = append(rb.data, v)
}

// Seed replaces the buffer's contents with a single observation. Used during
// restart recovery when we have one persisted value to reseed from.
func (rb *RingBuffer) Seed(v float64) {
	rb.data = append(rb.data[:0], v)
}

// Values returns the underlying slice in insertion order. Callers MUST NOT
// mutate the returned slice.
func (rb *RingBuffer) Values() []float64 { return rb.data }

// Median returns the median of the buffer's values. Empty buffer returns 0.
func (rb *RingBuffer) Median() float64 {
	n := len(rb.data)
	if n == 0 {
		return 0
	}
	sorted := make([]float64, n)
	copy(sorted, rb.data)
	sort.Float64s(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// PerCRState holds the in-memory state for one AgenticAutoscaler CR.
//
// The two cooldown-time fields are tracked separately because design §5
// uses different cooldowns for up/down events; the most recent event of
// either kind resets only its own clock.
type PerCRState struct {
	Observations      *RingBuffer
	RpsPerPod         float64
	LastScaleUpTime   time.Time
	LastScaleDownTime time.Time
	// Initialized is set true once InitializeFromStatus has run, so
	// subsequent reconciles know not to re-seed from the (possibly stale)
	// status fields.
	Initialized bool
}

// StateStore is a concurrency-safe map of per-CR states. Reconcile is
// serialised per-key by controller-runtime, but multiple CRs may reconcile
// concurrently, so the outer map needs a sync.RWMutex.
type StateStore struct {
	mu    sync.RWMutex
	store map[types.NamespacedName]*PerCRState
}

// NewStateStore returns an empty StateStore.
func NewStateStore() *StateStore {
	return &StateStore{store: make(map[types.NamespacedName]*PerCRState)}
}

// Get returns the per-CR state for key, or nil if absent.
func (s *StateStore) Get(key types.NamespacedName) *PerCRState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store[key]
}

// GetOrCreate returns the existing per-CR state for key, or constructs a
// fresh PerCRState (with an empty ring buffer of the requested capacity)
// and stores it. The returned pointer is shared — callers must coordinate
// access externally (controller-runtime's per-key reconcile ordering is
// sufficient for the controller use case).
func (s *StateStore) GetOrCreate(key types.NamespacedName, capacity int) *PerCRState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.store[key]; ok {
		return st
	}
	st := &PerCRState{Observations: NewRingBuffer(capacity)}
	s.store[key] = st
	return st
}

// Delete removes the per-CR state for key. Used when a CR is deleted to
// avoid leaking state for vanished resources.
func (s *StateStore) Delete(key types.NamespacedName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.store, key)
}
