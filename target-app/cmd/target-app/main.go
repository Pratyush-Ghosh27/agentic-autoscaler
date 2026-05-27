// target-app is the instrumented HTTP server scaled by the agentic
// autoscaler's controlled experiment. See docs/design_v2.md §3.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/pratyush-ghosh/agentic-autoscaler/target-app/internal/server"
)

func main() {
	cfg := server.LoadConfig()
	srv := server.New(cfg)

	port := os.Getenv("TARGET_PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

	log.Printf("target-app starting: addr=%s concurrency=%d work_duration_ms=%d work_jitter_ms=%d",
		addr, cfg.Concurrency, cfg.WorkDurationMS, cfg.WorkJitterMS)

	// Explicit timeouts so the load-bearing HTTP server isn't vulnerable
	// to slow-loris-style stalls (and so gosec G114 stays happy).
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if err := httpSrv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
