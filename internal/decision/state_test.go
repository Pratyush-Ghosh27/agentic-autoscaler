/*
Copyright 2026.
*/

package decision_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/decision"
)

// -----------------------------------------------------------------------
// RingBuffer
// -----------------------------------------------------------------------

func TestRingBuffer_MedianOddCount(t *testing.T) {
	rb := decision.NewRingBuffer(10)
	rb.Push(100)
	rb.Push(200)
	rb.Push(150)
	assert.InDelta(t, 150.0, rb.Median(), 0.001)
}

func TestRingBuffer_MedianEvenCount(t *testing.T) {
	rb := decision.NewRingBuffer(10)
	rb.Push(100)
	rb.Push(200)
	assert.InDelta(t, 150.0, rb.Median(), 0.001)
}

func TestRingBuffer_EvictsOldest(t *testing.T) {
	rb := decision.NewRingBuffer(3)
	rb.Push(100)
	rb.Push(200)
	rb.Push(300)
	rb.Push(400) // evicts 100
	assert.Len(t, rb.Values(), 3)
	assert.InDelta(t, 300.0, rb.Median(), 0.001)
}

func TestRingBuffer_EmptyReturnsZero(t *testing.T) {
	rb := decision.NewRingBuffer(10)
	assert.Equal(t, 0.0, rb.Median())
}

func TestRingBuffer_SingleElement(t *testing.T) {
	rb := decision.NewRingBuffer(10)
	rb.Push(42)
	assert.InDelta(t, 42.0, rb.Median(), 0.001)
}

func TestRingBuffer_SeedReplacesContents(t *testing.T) {
	rb := decision.NewRingBuffer(10)
	rb.Push(10)
	rb.Push(20)
	rb.Seed(175)
	require.Len(t, rb.Values(), 1)
	assert.InDelta(t, 175.0, rb.Median(), 0.001)
}

func TestRingBuffer_SeedN_PushesNCopies(t *testing.T) {
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

// TestRingBuffer_SeedN_PreservesMedianAcrossFreshObservations is the
// behavioural pin for F20: with five seed copies and four fresh
// observations of a different value, the median of nine entries lands
// exactly on the persisted seed value (sorted: [200,200,200,200,seed,
// seed,seed,seed,seed], median at index 4 = seed). This is what makes
// "preserves persisted estimate across the next 5+ observations" true.
func TestRingBuffer_SeedN_PreservesMedianAcrossFreshObservations(t *testing.T) {
	rb := decision.NewRingBuffer(10)
	rb.SeedN(175, 5)
	for i := 0; i < 4; i++ {
		rb.Push(200)
	}
	require.Len(t, rb.Values(), 9)
	assert.InDelta(t, 175.0, rb.Median(), 0.001,
		"5-copy seed must dominate the median across 4 fresh observations")
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

func TestRingBuffer_FillToCap(t *testing.T) {
	rb := decision.NewRingBuffer(5)
	for i := 1; i <= 5; i++ {
		rb.Push(float64(i * 10))
	}
	assert.InDelta(t, 30.0, rb.Median(), 0.001)
	assert.Len(t, rb.Values(), 5)
}

// -----------------------------------------------------------------------
// StateStore
// -----------------------------------------------------------------------

func TestStateStore_GetMissingReturnsNil(t *testing.T) {
	s := decision.NewStateStore()
	assert.Nil(t, s.Get(types.NamespacedName{Namespace: "ns", Name: "missing"}))
}

func TestStateStore_GetOrCreateReturnsSameInstance(t *testing.T) {
	s := decision.NewStateStore()
	key := types.NamespacedName{Namespace: "ns", Name: "demo"}

	st1 := s.GetOrCreate(key, 10)
	st2 := s.GetOrCreate(key, 10)
	assert.Same(t, st1, st2, "GetOrCreate should return the same pointer on subsequent calls")
}

func TestStateStore_GetOrCreateInitialState(t *testing.T) {
	s := decision.NewStateStore()
	st := s.GetOrCreate(types.NamespacedName{Namespace: "ns", Name: "demo"}, 10)
	assert.NotNil(t, st.Observations)
	assert.False(t, st.Initialized)
	assert.Equal(t, 0.0, st.RpsPerPod)
}

func TestStateStore_Delete(t *testing.T) {
	s := decision.NewStateStore()
	key := types.NamespacedName{Namespace: "ns", Name: "demo"}
	s.GetOrCreate(key, 10)
	require.NotNil(t, s.Get(key))
	s.Delete(key)
	assert.Nil(t, s.Get(key))
}

func TestStateStore_ConcurrentGetOrCreate(t *testing.T) {
	// Concurrent GetOrCreate on the same key must return the same instance.
	s := decision.NewStateStore()
	key := types.NamespacedName{Namespace: "ns", Name: "concurrent"}

	const N = 50
	results := make([]*decision.PerCRState, N)
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx] = s.GetOrCreate(key, 10)
		}(i)
	}
	wg.Wait()

	first := results[0]
	for i := 1; i < N; i++ {
		assert.Same(t, first, results[i], "all goroutines saw same instance")
	}
}
