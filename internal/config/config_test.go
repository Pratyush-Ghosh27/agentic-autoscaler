package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withRequiredEnv sets both required vars so LoadFromEnv doesn't bail early.
func withRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("FORECAST_SERVICE_URL", "http://fc.test:8000")
	t.Setenv("PROMETHEUS_URL", "http://prom.test:9090")
}

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

func TestLoadFromEnv_HotPathDefaults(t *testing.T) {
	withRequiredEnv(t)

	cfg, err := LoadFromEnv()

	require.NoError(t, err)
	assert.Equal(t, 60*time.Second, cfg.ReconcileInterval)
	assert.Equal(t, 60*time.Minute, cfg.HotPathHistory)
	assert.Equal(t, int32(10), cfg.HotPathMinPoints)
	assert.Equal(t, 10*time.Minute, cfg.ForecastHorizon)
	assert.Equal(t, 5*time.Second, cfg.ForecastTimeout)
	assert.Equal(t, int32(60), cfg.ProphetMinPoints)
}

func TestLoadFromEnv_HotPathOverrides(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("RECONCILE_INTERVAL_SECONDS", "30")
	t.Setenv("HOT_PATH_HISTORY_MINUTES", "120")
	t.Setenv("HOT_PATH_MIN_POINTS", "5")
	t.Setenv("FORECAST_HORIZON_MINUTES", "15")
	t.Setenv("FORECAST_TIMEOUT_SECONDS", "10")
	t.Setenv("PROPHET_MIN_POINTS", "90")

	cfg, err := LoadFromEnv()

	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, cfg.ReconcileInterval)
	assert.Equal(t, 120*time.Minute, cfg.HotPathHistory)
	assert.Equal(t, int32(5), cfg.HotPathMinPoints)
	assert.Equal(t, 15*time.Minute, cfg.ForecastHorizon)
	assert.Equal(t, 10*time.Second, cfg.ForecastTimeout)
	assert.Equal(t, int32(90), cfg.ProphetMinPoints)
}
