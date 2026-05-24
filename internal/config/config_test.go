package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadFromEnv_RequiresForecastServiceURL(t *testing.T) {
	t.Setenv("PROMETHEUS_URL", "http://prom.test:9090")

	_, err := LoadFromEnv()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FORECAST_SERVICE_URL")
}

func TestLoadFromEnv_RequiresPrometheusURL(t *testing.T) {
	t.Setenv("FORECAST_SERVICE_URL", "http://fc.test:8000")

	_, err := LoadFromEnv()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "PROMETHEUS_URL")
}

func TestLoadFromEnv_AcceptsBothRequiredVars(t *testing.T) {
	t.Setenv("FORECAST_SERVICE_URL", "http://fc.test:8000")
	t.Setenv("PROMETHEUS_URL", "http://prom.test:9090")

	cfg, err := LoadFromEnv()

	require.NoError(t, err)
	assert.Equal(t, "http://fc.test:8000", cfg.ForecastServiceURL)
	assert.Equal(t, "http://prom.test:9090", cfg.PrometheusURL)
}
