// Package server is the HTTP server for the target-app load target.
// See docs/design.md §3 for the controlled experiment context.
package server

import (
	"net/http"
	"sync/atomic"
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
	cfg   Config
	ready atomic.Bool
}

// New constructs a Server with the given config.
func New(cfg Config) *Server {
	s := &Server{cfg: cfg}
	s.ready.Store(true)
	return s
}

// SetReady toggles the readiness probe response. Used by tests and by
// any future graceful-shutdown path.
func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
}

// Handler returns the http.Handler exposing all routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if !s.ready.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"not_ready"}`))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ready"}`))
}
