/*
Copyright 2026.
*/

package controller_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	controller "github.com/pratyush-ghosh/agentic-autoscaler/internal/controller"
)

// TestChannelNotifier_DropAndReplace pins the design.md §6.2 contract:
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
