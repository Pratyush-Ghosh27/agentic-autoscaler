/*
Copyright 2026.
*/

package controller_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	controller "github.com/pratyush-ghosh/agentic-autoscaler/internal/controller"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/reasoning"
)

// TestChannelNotifier_DropAndReplace pins the design_v2.md §6.2 contract:
// a queued event must be replaced by the newer one, not retained or buffered.
func TestChannelNotifier_DropAndReplace(t *testing.T) {
	ch := make(chan controller.ExplainRequest, 1)
	cn := controller.ChannelNotifier{Ch: ch}

	cn.Notify(controller.ExplainRequest{Reason: "scale_up", TargetReplicas: 5})
	cn.Notify(controller.ExplainRequest{Reason: "scale_up", TargetReplicas: 8})

	req := <-ch
	assert.Equal(t, int32(8), req.TargetReplicas, "newer event survives")

	select {
	case extra := <-ch:
		t.Fatalf("channel should be empty, got %+v", extra)
	default:
	}
}

func TestChannelNotifier_EmptyChannelPath(t *testing.T) {
	ch := make(chan controller.ExplainRequest, 1)
	cn := controller.ChannelNotifier{Ch: ch}

	cn.Notify(controller.ExplainRequest{Reason: "scale_down", TargetReplicas: 2})

	req := <-ch
	require.Equal(t, "scale_down", req.Reason)
	require.Equal(t, int32(2), req.TargetReplicas)
}

// TestChannelNotifier_ManySends covers the steady-state case where many
// reconciles fire faster than a worker drains: every old event must be
// dropped, only the very last one survives.
func TestChannelNotifier_ManySends(t *testing.T) {
	ch := make(chan controller.ExplainRequest, 1)
	cn := controller.ChannelNotifier{Ch: ch}

	for i := int32(1); i <= 100; i++ {
		cn.Notify(controller.ExplainRequest{TargetReplicas: i})
	}
	req := <-ch
	assert.Equal(t, int32(100), req.TargetReplicas)
}

// TestExplainRequest_HasG18Fields is a compile-time pin: the six fields
// the G18 prompt template (Task 11-13) consumes must exist on
// ExplainRequest with the documented types. If any field is renamed or
// the type changes, this test breaks the build.
func TestExplainRequest_HasG18Fields(t *testing.T) {
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

// TestChannelNotifier_DropAndReplaceWithBindingToken pins the contract
// against the G13 MaxReplicasBinding token: when a stale scale_up event
// is followed by a max_replicas_binding event (which can fire in the same
// reconcile cycle if the forecast jumps over the CRD bound between
// observations), the binding event must survive — operators must see the
// most recent capacity-planning signal.
func TestChannelNotifier_DropAndReplaceWithBindingToken(t *testing.T) {
	ch := make(chan controller.ExplainRequest, 1)
	cn := controller.ChannelNotifier{Ch: ch}

	stale := controller.ExplainRequest{Reason: reasoning.ScaleUp, TargetReplicas: 3}
	fresh := controller.ExplainRequest{Reason: reasoning.MaxReplicasBinding, TargetReplicas: 5}

	cn.Notify(stale)
	cn.Notify(fresh)

	require.Len(t, ch, 1)
	got := <-ch
	assert.Equal(t, reasoning.MaxReplicasBinding, got.Reason,
		"the most recent notify must be the one consumed")
	assert.Equal(t, int32(5), got.TargetReplicas)
}
