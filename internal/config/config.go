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

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("config validation failed: %v", errs)
	}
	return cfg, nil
}
