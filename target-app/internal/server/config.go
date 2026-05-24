package server

import (
	"os"
	"strconv"
)

// LoadConfig reads target-app config from env vars, falling back to
// DefaultConfig() values for anything missing or unparseable.
func LoadConfig() Config {
	cfg := DefaultConfig()
	cfg.Concurrency = envIntOrDefault("TARGET_CONCURRENCY", cfg.Concurrency)
	cfg.WorkDurationMS = envIntOrDefault("TARGET_WORK_DURATION_MS", cfg.WorkDurationMS)
	cfg.WorkJitterMS = envIntOrDefault("TARGET_WORK_JITTER_MS", cfg.WorkJitterMS)
	return cfg
}

// envIntOrDefault returns the int value of the env var, or def if unset,
// unparseable, or negative. Concurrency=0 is permitted and useful for
// deterministic 503 testing; negative values are silently rejected.
func envIntOrDefault(name string, def int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if v < 0 {
		return def
	}
	return v
}
