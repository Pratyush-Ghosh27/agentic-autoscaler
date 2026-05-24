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

func TestLoadFromEnv_ColdPathDefaults(t *testing.T) {
	withRequiredEnv(t)

	cfg, err := LoadFromEnv()

	require.NoError(t, err)
	assert.Equal(t, 30*time.Minute, cfg.ClassifierInterval)
	assert.Equal(t, 24*time.Hour, cfg.ClassifierHistory)
	assert.Equal(t, int32(70), cfg.ClassifierMinPoints)
	assert.Equal(t, int32(240), cfg.ClassifierHighConfidencePoints)
	assert.Equal(t, 60*time.Second, cfg.ClassifierDedup)
}

func TestLoadFromEnv_PreClassificationDefaults(t *testing.T) {
	withRequiredEnv(t)

	cfg, err := LoadFromEnv()

	require.NoError(t, err)
	assert.Equal(t, 60*time.Second, cfg.DefaultScaleUpCooldown)
	assert.Equal(t, 300*time.Second, cfg.DefaultScaleDownCooldown)
	assert.Equal(t, int32(4), cfg.DefaultMaxStepSize)
}

func TestLoadFromEnv_OllamaDefaults(t *testing.T) {
	withRequiredEnv(t)

	cfg, err := LoadFromEnv()

	require.NoError(t, err)
	assert.Equal(t, "http://localhost:11434", cfg.OllamaURL)
	assert.Equal(t, "llama3.2", cfg.OllamaModel)
	assert.Equal(t, 30*time.Second, cfg.OllamaTimeout)
	assert.Equal(t, int32(150), cfg.OllamaMaxTokens)
}

func TestLoadFromEnv_OllamaOverrides(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("OLLAMA_URL", "http://ollama.svc:11434")
	t.Setenv("OLLAMA_MODEL", "phi3")
	t.Setenv("OLLAMA_TIMEOUT_SECONDS", "60")
	t.Setenv("OLLAMA_MAX_TOKENS", "300")

	cfg, err := LoadFromEnv()

	require.NoError(t, err)
	assert.Equal(t, "http://ollama.svc:11434", cfg.OllamaURL)
	assert.Equal(t, "phi3", cfg.OllamaModel)
	assert.Equal(t, 60*time.Second, cfg.OllamaTimeout)
	assert.Equal(t, int32(300), cfg.OllamaMaxTokens)
}

func TestLoadFromEnv_RejectsClassifierMinPointsBelow70(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("CLASSIFIER_MIN_POINTS", "50")

	_, err := LoadFromEnv()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "CLASSIFIER_MIN_POINTS")
	assert.Contains(t, err.Error(), "70")
}

func TestLoadFromEnv_RejectsNegativeCooldowns(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("DEFAULT_SCALE_UP_COOLDOWN_SECONDS", "-1")

	_, err := LoadFromEnv()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "DEFAULT_SCALE_UP_COOLDOWN_SECONDS")
}

func TestLoadFromEnv_RejectsZeroMaxStepSize(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("DEFAULT_MAX_STEP_SIZE", "0")

	_, err := LoadFromEnv()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "DEFAULT_MAX_STEP_SIZE")
}

func TestLoadFromEnv_RejectsHotPathMinPointsZero(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("HOT_PATH_MIN_POINTS", "0")

	_, err := LoadFromEnv()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "HOT_PATH_MIN_POINTS")
}

func TestLoadFromEnv_RejectsHighConfidenceBelowMinPoints(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("CLASSIFIER_MIN_POINTS", "100")
	t.Setenv("CLASSIFIER_HIGH_CONFIDENCE_POINTS", "80")

	_, err := LoadFromEnv()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "CLASSIFIER_HIGH_CONFIDENCE_POINTS")
	assert.Contains(t, err.Error(), "CLASSIFIER_MIN_POINTS")
}

func TestLoadFromEnv_AcceptsAllValidOverrides(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("CLASSIFIER_MIN_POINTS", "100")
	t.Setenv("CLASSIFIER_HIGH_CONFIDENCE_POINTS", "300")
	t.Setenv("DEFAULT_SCALE_UP_COOLDOWN_SECONDS", "0")
	t.Setenv("DEFAULT_MAX_STEP_SIZE", "1")

	cfg, err := LoadFromEnv()

	require.NoError(t, err)
	assert.Equal(t, int32(100), cfg.ClassifierMinPoints)
	assert.Equal(t, int32(300), cfg.ClassifierHighConfidencePoints)
	assert.Equal(t, time.Duration(0), cfg.DefaultScaleUpCooldown)
	assert.Equal(t, int32(1), cfg.DefaultMaxStepSize)
}
