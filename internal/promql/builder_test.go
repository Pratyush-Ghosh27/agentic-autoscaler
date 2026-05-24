/*
Copyright 2026.
*/

package promql_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/promql"
)

func TestInstantRPSQuery(t *testing.T) {
	got := promql.InstantRPS("demo")
	assert.Equal(t, `sum(rate(http_requests_total{deployment="demo"}[2m]))`, got)
}

func TestInstantRPSQuery_HyphenatedName(t *testing.T) {
	got := promql.InstantRPS("my-app-v2")
	assert.Equal(t, `sum(rate(http_requests_total{deployment="my-app-v2"}[2m]))`, got)
}

func TestRangeRPSQuery_MatchesInstantExpression(t *testing.T) {
	// The range query reuses the exact same expression — start/end/step
	// are URL parameters at the adapter level, not PromQL.
	got := promql.RangeRPS("demo")
	assert.Equal(t, promql.InstantRPS("demo"), got)
}
