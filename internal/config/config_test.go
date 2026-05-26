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
	assert.Equal(t, 5*time.Second, cfg.ForecastTimeout)
	// PROPHET_MIN_POINTS default lowered from 60 to 30 (F2a-revisited);
	// the service now learns from short histories and Prophet's own
	// gating handles low-confidence cases.
	assert.Equal(t, int32(30), cfg.ProphetMinPoints)
}

func TestLoadFromEnv_HotPathOverrides(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("RECONCILE_INTERVAL_SECONDS", "30")
	t.Setenv("HOT_PATH_HISTORY_MINUTES", "120")
	t.Setenv("HOT_PATH_MIN_POINTS", "5")
	t.Setenv("FORECAST_TIMEOUT_SECONDS", "10")
	t.Setenv("PROPHET_MIN_POINTS", "90")

	cfg, err := LoadFromEnv()

	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, cfg.ReconcileInterval)
	assert.Equal(t, 120*time.Minute, cfg.HotPathHistory)
	assert.Equal(t, int32(5), cfg.HotPathMinPoints)
	assert.Equal(t, 10*time.Second, cfg.ForecastTimeout)
	assert.Equal(t, int32(90), cfg.ProphetMinPoints)
}

func TestLoadFromEnv_ColdPathDefaults(t *testing.T) {
	withRequiredEnv(t)

	cfg, err := LoadFromEnv()

	require.NoError(t, err)
	assert.Equal(t, 30*time.Minute, cfg.ClassifierInterval)
	assert.Equal(t, 24*time.Hour, cfg.ClassifierHistory)
	// CLASSIFIER_MIN_POINTS default raised from 70 to 72 per
	// v2 F2a-revisited (5 days at 5-min cadence ÷ 24-hour stride).
	assert.Equal(t, int32(72), cfg.ClassifierMinPoints)
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

// At the v2 default resolution (5 minutes), the L+10 floor is 22.
// MIN_POINTS=21 must fail; this replaces the v1 fixed-floor-of-70 test
// (covered now by TestValidate_ClassifierMinPointsFloorAt1MinResolution
// for the resolution=1 case).
func TestLoadFromEnv_RejectsClassifierMinPointsBelowResolutionFloor(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("CLASSIFIER_MIN_POINTS", "21")

	_, err := LoadFromEnv()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "CLASSIFIER_MIN_POINTS")
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

// TestLoadFromEnv_NewV2EnvVars pins T4 env-var realignment per G21.
// Adds CONTEXT_DOWNSAMPLE_RESOLUTION_MIN, CV_GUARD_MEAN_RPS,
// RPS_PER_POD_NOISE_FLOOR_RPS, HOURLY_PROFILE_MIN_HOURS; raises
// CLASSIFIER_MIN_POINTS default to 72 (F2a-revisited).
func TestLoadFromEnv_NewV2EnvVars(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("CONTEXT_DOWNSAMPLE_RESOLUTION_MIN", "5")
	t.Setenv("CV_GUARD_MEAN_RPS", "1.0")
	t.Setenv("RPS_PER_POD_NOISE_FLOOR_RPS", "10")
	t.Setenv("HOURLY_PROFILE_MIN_HOURS", "12")

	cfg, err := LoadFromEnv()
	require.NoError(t, err)

	assert.Equal(t, int32(5), cfg.ContextResolutionMinutes)
	assert.InDelta(t, 1.0, cfg.CVGuardMeanRPS, 0.001)
	assert.Equal(t, int32(10), cfg.RpsPerPodNoiseFloorRPS)
	assert.Equal(t, int32(12), cfg.HourlyProfileMinHours)
	assert.Equal(t, int32(72), cfg.ClassifierMinPoints, "F2a-revisited: default raised from 70 to 72")
	assert.Equal(t, int32(240), cfg.ClassifierHighConfidencePoints)
}

// TestLoadFromEnv_NewV2EnvVarsHaveDefaults pins that all four new
// v2 env vars have sensible defaults so existing operators upgrade
// without setting anything.
func TestLoadFromEnv_NewV2EnvVarsHaveDefaults(t *testing.T) {
	withRequiredEnv(t)

	cfg, err := LoadFromEnv()
	require.NoError(t, err)

	assert.Equal(t, int32(5), cfg.ContextResolutionMinutes)
	assert.InDelta(t, 1.0, cfg.CVGuardMeanRPS, 0.001)
	assert.Equal(t, int32(10), cfg.RpsPerPodNoiseFloorRPS)
	assert.Equal(t, int32(12), cfg.HourlyProfileMinHours)
}

// TestValidate_ClassifierMinPointsFloorTracksResolution: at
// resolution=5, the L+10 floor is 22, so MIN_POINTS=22 must pass.
func TestValidate_ClassifierMinPointsFloorTracksResolution(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("CONTEXT_DOWNSAMPLE_RESOLUTION_MIN", "5")
	t.Setenv("CLASSIFIER_MIN_POINTS", "22")
	t.Setenv("CLASSIFIER_HIGH_CONFIDENCE_POINTS", "240")

	_, err := LoadFromEnv()
	require.NoError(t, err, "MIN_POINTS=22 at resolution=5 should be valid (L+10=22)")
}

// TestValidate_ClassifierMinPointsFloorRejectsTooLow: 21 < 22.
func TestValidate_ClassifierMinPointsFloorRejectsTooLow(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("CONTEXT_DOWNSAMPLE_RESOLUTION_MIN", "5")
	t.Setenv("CLASSIFIER_MIN_POINTS", "21")

	_, err := LoadFromEnv()
	require.Error(t, err, "MIN_POINTS=21 at resolution=5 should fail validation (L+10=22)")
	assert.Contains(t, err.Error(), "CLASSIFIER_MIN_POINTS")
}

// TestValidate_ClassifierMinPointsFloorAt1MinResolution: at
// resolution=1, the L+10 floor is 70, matching the legacy v1 floor.
func TestValidate_ClassifierMinPointsFloorAt1MinResolution(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("CONTEXT_DOWNSAMPLE_RESOLUTION_MIN", "1")
	t.Setenv("CLASSIFIER_MIN_POINTS", "69")

	_, err := LoadFromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "70")
}

// TestValidate_RejectsZeroResolution.
func TestValidate_RejectsZeroResolution(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("CONTEXT_DOWNSAMPLE_RESOLUTION_MIN", "0")

	_, err := LoadFromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CONTEXT_DOWNSAMPLE_RESOLUTION_MIN")
}

// TestValidate_RejectsBadHourlyProfileMinHours.
func TestValidate_RejectsBadHourlyProfileMinHours(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("HOURLY_PROFILE_MIN_HOURS", "0")

	_, err := LoadFromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HOURLY_PROFILE_MIN_HOURS")
}

func TestConfigSummary_StableShape(t *testing.T) {
	withRequiredEnv(t)
	cfg, err := LoadFromEnv()
	require.NoError(t, err)

	summary := cfg.Summary()

	assert.Contains(t, summary, "FORECAST_SERVICE_URL")
	assert.Contains(t, summary, "PROMETHEUS_URL")
	assert.Contains(t, summary, "RECONCILE_INTERVAL_SECONDS=60")
	assert.Contains(t, summary, "CLASSIFIER_MIN_POINTS=72")
	assert.Contains(t, summary, "OLLAMA_MODEL=llama3.2")
	assert.Contains(t, summary, "CONTEXT_DOWNSAMPLE_RESOLUTION_MIN=5")
	assert.Contains(t, summary, "CV_GUARD_MEAN_RPS=1")
	assert.Contains(t, summary, "RPS_PER_POD_NOISE_FLOOR_RPS=10")
	assert.Contains(t, summary, "HOURLY_PROFILE_MIN_HOURS=12")
	// FORECAST_HORIZON_MINUTES is service-only per F36; no longer in
	// the controller summary.
	assert.NotContains(t, summary, "FORECAST_HORIZON_MINUTES")
}
