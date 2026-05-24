// Package server is the HTTP server for the target-app load target.
// See docs/design.md §3 for the controlled experiment context.
package server

import (
	"net/http"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	cfg       Config
	ready     atomic.Bool
	registry  *prometheus.Registry
	histogram *prometheus.HistogramVec
	counter   *prometheus.CounterVec
}

// New constructs a Server with the given config.
func New(cfg Config) *Server {
	reg := prometheus.NewRegistry()

	histogram := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "target_app_request_duration_seconds",
			Help: "End-to-end request duration in seconds.",
			Buckets: []float64{
				0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1,
				0.25, 0.5, 1, 2.5, 5, 10,
			},
		},
		[]string{"path"},
	)

	counter := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "target_app_requests_total",
			Help: "Total number of requests, labeled by status.",
		},
		[]string{"path", "status"},
	)

	reg.MustRegister(histogram, counter)

	// Pre-instantiate the /work labels so /metrics renders the histogram
	// definition + bucket lines and the counter rows from the very first
	// scrape, even before any /work requests have been received.
	histogram.WithLabelValues("/work")
	counter.WithLabelValues("/work", "200")
	counter.WithLabelValues("/work", "503")

	s := &Server{
		cfg:       cfg,
		registry:  reg,
		histogram: histogram,
		counter:   counter,
	}
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
	mux.Handle("/metrics", promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{Registry: s.registry}))
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
