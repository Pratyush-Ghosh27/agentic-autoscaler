// Package server is the HTTP server for the target-app load target.
// See docs/design.md §3 for the controlled experiment context.
package server

import (
	"net/http"
)

// Config controls server behaviour.
type Config struct {
	Concurrency    int
	WorkDurationMS int
	WorkJitterMS   int
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		Concurrency:    8,
		WorkDurationMS: 50,
		WorkJitterMS:   30,
	}
}

// Server is the instrumented HTTP server.
type Server struct {
	cfg Config
}

// New constructs a Server with the given config.
func New(cfg Config) *Server {
	return &Server{cfg: cfg}
}

// Handler returns the http.Handler exposing all routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
