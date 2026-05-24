// target-app is the instrumented HTTP server scaled by the agentic
// autoscaler's controlled experiment. See docs/design.md §3.
package main

import (
	"log"
	"net/http"
	"os"

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

	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
