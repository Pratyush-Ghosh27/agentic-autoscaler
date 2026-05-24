package server_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pratyush-ghosh/agentic-autoscaler/target-app/internal/server"
)

func TestLoadConfig_Defaults(t *testing.T) {
	cfg := server.LoadConfig()
	assert.Equal(t, 8, cfg.Concurrency)
	assert.Equal(t, 50, cfg.WorkDurationMS)
	assert.Equal(t, 30, cfg.WorkJitterMS)
}

func TestLoadConfig_Overrides(t *testing.T) {
	t.Setenv("TARGET_CONCURRENCY", "4")
	t.Setenv("TARGET_WORK_DURATION_MS", "100")
	t.Setenv("TARGET_WORK_JITTER_MS", "0")

	cfg := server.LoadConfig()
	assert.Equal(t, 4, cfg.Concurrency)
	assert.Equal(t, 100, cfg.WorkDurationMS)
	assert.Equal(t, 0, cfg.WorkJitterMS)
}

func TestLoadConfig_NegativeOrInvalidFallsToDefault(t *testing.T) {
	t.Setenv("TARGET_CONCURRENCY", "-1")
	t.Setenv("TARGET_WORK_DURATION_MS", "garbage")

	cfg := server.LoadConfig()
	assert.Equal(t, 8, cfg.Concurrency, "negative value should fall to default")
	assert.Equal(t, 50, cfg.WorkDurationMS, "garbage should fall to default")
}
