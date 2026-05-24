// Package config loads the controller's runtime parameters from
// environment variables. Defaults match docs/design.md §4 exactly.
// Two vars are required (FORECAST_SERVICE_URL, PROMETHEUS_URL); all
// others have sensible defaults.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config is the fully resolved, validated controller configuration.
type Config struct {
	// Required.
	ForecastServiceURL string
	PrometheusURL      string

	// Hot-path timing.
	ReconcileInterval time.Duration // RECONCILE_INTERVAL_SECONDS, default 60s
	HotPathHistory    time.Duration // HOT_PATH_HISTORY_MINUTES, default 60m
	HotPathMinPoints  int32         // HOT_PATH_MIN_POINTS, default 10
	ForecastHorizon   time.Duration // FORECAST_HORIZON_MINUTES, default 10m
	ForecastTimeout   time.Duration // FORECAST_TIMEOUT_SECONDS, default 5s
	ProphetMinPoints  int32         // PROPHET_MIN_POINTS, default 60

	// Cold-path timing.
	ClassifierInterval             time.Duration // CLASSIFIER_INTERVAL_MINUTES, default 30m
	ClassifierHistory              time.Duration // CLASSIFIER_HISTORY_HOURS, default 24h
	ClassifierMinPoints            int32         // CLASSIFIER_MIN_POINTS, default 70 (must be >= 70 per §7)
	ClassifierHighConfidencePoints int32         // CLASSIFIER_HIGH_CONFIDENCE_POINTS, default 240
	ClassifierDedup                time.Duration // CLASSIFIER_DEDUP_SECONDS, default 60s

	// Pre-classification reconcile defaults.
	DefaultScaleUpCooldown   time.Duration // DEFAULT_SCALE_UP_COOLDOWN_SECONDS, default 60s
	DefaultScaleDownCooldown time.Duration // DEFAULT_SCALE_DOWN_COOLDOWN_SECONDS, default 300s
	DefaultMaxStepSize       int32         // DEFAULT_MAX_STEP_SIZE, default 4

	// Ollama (ExplainWorker).
	OllamaURL       string        // OLLAMA_URL, default http://localhost:11434
	OllamaModel     string        // OLLAMA_MODEL, default llama3.2
	OllamaTimeout   time.Duration // OLLAMA_TIMEOUT_SECONDS, default 30s
	OllamaMaxTokens int32         // OLLAMA_MAX_TOKENS, default 150
}

// envIntOrDefault reads an integer env var, returns the default if unset.
// Records a parse error in errs (and returns the default) if the var is set
// but unparseable.
func envIntOrDefault(name string, def int32, errs *[]string) int32 {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s: %v", name, err))
		return def
	}
	return int32(v)
}

// envSecondsOrDefault reads a seconds-valued env var as a Duration.
func envSecondsOrDefault(name string, def time.Duration, errs *[]string) time.Duration {
	v := envIntOrDefault(name, int32(def/time.Second), errs)
	return time.Duration(v) * time.Second
}

// envMinutesOrDefault reads a minutes-valued env var as a Duration.
func envMinutesOrDefault(name string, def time.Duration, errs *[]string) time.Duration {
	v := envIntOrDefault(name, int32(def/time.Minute), errs)
	return time.Duration(v) * time.Minute
}

// envHoursOrDefault reads an hours-valued env var as a Duration.
func envHoursOrDefault(name string, def time.Duration, errs *[]string) time.Duration {
	v := envIntOrDefault(name, int32(def/time.Hour), errs)
	return time.Duration(v) * time.Hour
}

// envStringOrDefault returns the env var or def if unset.
func envStringOrDefault(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// LoadFromEnv reads the controller config from environment variables.
// Returns an error listing every problem found, so a misconfigured
// operator sees all issues at once rather than fixing them one at a time.
func LoadFromEnv() (Config, error) {
	cfg := Config{}
	var errs []string

	if cfg.ForecastServiceURL = os.Getenv("FORECAST_SERVICE_URL"); cfg.ForecastServiceURL == "" {
		errs = append(errs, "FORECAST_SERVICE_URL is required")
	}
	if cfg.PrometheusURL = os.Getenv("PROMETHEUS_URL"); cfg.PrometheusURL == "" {
		errs = append(errs, "PROMETHEUS_URL is required")
	}

	cfg.ReconcileInterval = envSecondsOrDefault("RECONCILE_INTERVAL_SECONDS", 60*time.Second, &errs)
	cfg.HotPathHistory = envMinutesOrDefault("HOT_PATH_HISTORY_MINUTES", 60*time.Minute, &errs)
	cfg.HotPathMinPoints = envIntOrDefault("HOT_PATH_MIN_POINTS", 10, &errs)
	cfg.ForecastHorizon = envMinutesOrDefault("FORECAST_HORIZON_MINUTES", 10*time.Minute, &errs)
	cfg.ForecastTimeout = envSecondsOrDefault("FORECAST_TIMEOUT_SECONDS", 5*time.Second, &errs)
	cfg.ProphetMinPoints = envIntOrDefault("PROPHET_MIN_POINTS", 60, &errs)

	cfg.ClassifierInterval = envMinutesOrDefault("CLASSIFIER_INTERVAL_MINUTES", 30*time.Minute, &errs)
	cfg.ClassifierHistory = envHoursOrDefault("CLASSIFIER_HISTORY_HOURS", 24*time.Hour, &errs)
	cfg.ClassifierMinPoints = envIntOrDefault("CLASSIFIER_MIN_POINTS", 70, &errs)
	cfg.ClassifierHighConfidencePoints = envIntOrDefault("CLASSIFIER_HIGH_CONFIDENCE_POINTS", 240, &errs)
	cfg.ClassifierDedup = envSecondsOrDefault("CLASSIFIER_DEDUP_SECONDS", 60*time.Second, &errs)

	cfg.DefaultScaleUpCooldown = envSecondsOrDefault("DEFAULT_SCALE_UP_COOLDOWN_SECONDS", 60*time.Second, &errs)
	cfg.DefaultScaleDownCooldown = envSecondsOrDefault("DEFAULT_SCALE_DOWN_COOLDOWN_SECONDS", 300*time.Second, &errs)
	cfg.DefaultMaxStepSize = envIntOrDefault("DEFAULT_MAX_STEP_SIZE", 4, &errs)

	cfg.OllamaURL = envStringOrDefault("OLLAMA_URL", "http://localhost:11434")
	cfg.OllamaModel = envStringOrDefault("OLLAMA_MODEL", "llama3.2")
	cfg.OllamaTimeout = envSecondsOrDefault("OLLAMA_TIMEOUT_SECONDS", 30*time.Second, &errs)
	cfg.OllamaMaxTokens = envIntOrDefault("OLLAMA_MAX_TOKENS", 150, &errs)

	errs = append(errs, cfg.validate()...)

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("config validation failed: %v", errs)
	}
	return cfg, nil
}

// validate runs cross-field bound checks. Per docs/design.md §4 and §7.
func (c Config) validate() []string {
	var errs []string

	// §7 hard floor for tod_correlation to be computable.
	if c.ClassifierMinPoints < 70 {
		errs = append(errs, fmt.Sprintf(
			"CLASSIFIER_MIN_POINTS=%d violates the design §7 floor of 70 (tod_correlation requires 60-point lag + 10 minimum overlap)",
			c.ClassifierMinPoints))
	}

	// High confidence must require at least as many points as medium confidence.
	if c.ClassifierHighConfidencePoints < c.ClassifierMinPoints {
		errs = append(errs, fmt.Sprintf(
			"CLASSIFIER_HIGH_CONFIDENCE_POINTS=%d must be >= CLASSIFIER_MIN_POINTS=%d",
			c.ClassifierHighConfidencePoints, c.ClassifierMinPoints))
	}

	// Step sizes must be at least 1 to allow any movement.
	if c.DefaultMaxStepSize < 1 {
		errs = append(errs, fmt.Sprintf(
			"DEFAULT_MAX_STEP_SIZE=%d must be >= 1", c.DefaultMaxStepSize))
	}

	// Min points for the hot path must be at least 1.
	if c.HotPathMinPoints < 1 {
		errs = append(errs, fmt.Sprintf(
			"HOT_PATH_MIN_POINTS=%d must be >= 1", c.HotPathMinPoints))
	}

	// Negative cooldowns are never sensible; zero is an explicit no-cooldown opt.
	if c.DefaultScaleUpCooldown < 0 {
		errs = append(errs, fmt.Sprintf(
			"DEFAULT_SCALE_UP_COOLDOWN_SECONDS=%d must be >= 0",
			int(c.DefaultScaleUpCooldown/time.Second)))
	}
	if c.DefaultScaleDownCooldown < 0 {
		errs = append(errs, fmt.Sprintf(
			"DEFAULT_SCALE_DOWN_COOLDOWN_SECONDS=%d must be >= 0",
			int(c.DefaultScaleDownCooldown/time.Second)))
	}

	return errs
}
