/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

//go:build integration

// Dry-run validation of the k6 scenarios. Each test:
//  1. Starts an in-process httptest server matching target-app's /work
//     contract.
//  2. Invokes `k6 run --vus=1 --iterations=5` with the scenario's env
//     vars dialed down to 1-second windows so the run completes in <30s.
//  3. Asserts k6 exits 0 (i.e. all check() assertions and thresholds
//     pass).
//
// Requires `k6` to be on PATH. Skipped when k6 is missing so the test
// still runs cleanly in environments without it.
//
// Run with:
//
//	go test -tags=integration -v ./k6/...
package k6_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func startTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/work", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestK6DryRun_Ramp(t *testing.T) {
	runK6Scenario(t, "scenarios/ramp.js")
}

func TestK6DryRun_Steady(t *testing.T) {
	runK6Scenario(t, "scenarios/steady.js")
}

func TestK6DryRun_Spiky(t *testing.T) {
	runK6Scenario(t, "scenarios/spiky.js")
}

func TestK6DryRun_Bursty(t *testing.T) {
	runK6Scenario(t, "scenarios/bursty.js")
}

func runK6Scenario(t *testing.T, script string) {
	t.Helper()

	if _, err := exec.LookPath("k6"); err != nil {
		t.Skipf("k6 not on PATH: %v", err)
	}

	srv := startTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "k6", "run",
		"--vus=1", "--iterations=5", "--no-color",
		script)
	cmd.Env = append(os.Environ(),
		"TARGET_AGENTIC_URL="+srv.URL,
		"TARGET_HPA_URL="+srv.URL,
		"RAMP_UP_DURATION=1s",
		"RAMP_HOLD_DURATION=1s",
		"RAMP_DOWN_DURATION=1s",
		"RAMP_RPS_PEAK=2",
		"STEADY_RPS=2",
		"STEADY_DURATION=3s",
		"SPIKE_BASE_RPS=1",
		"SPIKE_PEAK_RPS=2",
		"SPIKE_INTERVAL=2s",
		"SPIKE_DURATION=1s",
		"SPIKY_TOTAL_DURATION=5s",
		"BURST_SIZE=2",
		"BURST_MIN_INTERVAL=1",
		"BURST_MAX_INTERVAL=2",
		"BURSTY_TOTAL_DURATION=5s",
		"BURSTY_ITERATIONS=5",
	)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("k6 output:\n%s", string(out))
	}
	require.NoError(t, err, "k6 dry-run of %s failed", script)
}
