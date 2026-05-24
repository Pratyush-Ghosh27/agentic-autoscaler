// Package config loads the controller's runtime parameters from
// environment variables. Defaults match docs/design.md §4 exactly.
// Two vars are required (FORECAST_SERVICE_URL, PROMETHEUS_URL); all
// others have sensible defaults.
package config

import (
	"fmt"
	"os"
)

// Config is the fully resolved, validated controller configuration.
type Config struct {
	// Required.
	ForecastServiceURL string
	PrometheusURL      string
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

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("config validation failed: %v", errs)
	}
	return cfg, nil
}
