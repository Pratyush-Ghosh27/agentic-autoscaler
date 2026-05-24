// target-app is the instrumented HTTP server scaled by the agentic
// autoscaler's controlled experiment. See docs/design.md §3.
package main

import (
	"log"
	"net/http"

	"github.com/pratyush-ghosh/agentic-autoscaler/target-app/internal/server"
)

func main() {
	srv := server.New(server.DefaultConfig())
	addr := ":8080"
	log.Printf("target-app listening on %s", addr)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
