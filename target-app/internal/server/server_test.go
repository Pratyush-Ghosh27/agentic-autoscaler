package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pratyush-ghosh/agentic-autoscaler/target-app/internal/server"
)

func TestHealthz_ReturnsOK(t *testing.T) {
	srv := server.New(server.DefaultConfig())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "ok")
}

func TestReadyz_DefaultReturnsOK(t *testing.T) {
	srv := server.New(server.DefaultConfig())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestReadyz_FailingDependencyReturns503(t *testing.T) {
	srv := server.New(server.DefaultConfig())
	srv.SetReady(false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestReadyz_RecoversToReady(t *testing.T) {
	srv := server.New(server.DefaultConfig())
	srv.SetReady(false)
	srv.SetReady(true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestMetrics_ExposesHistogramAndCounter(t *testing.T) {
	srv := server.New(server.DefaultConfig())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)

	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "target_app_request_duration_seconds")
	assert.Contains(t, body, "target_app_requests_total")
	assert.True(t, strings.Contains(body, "# TYPE target_app_request_duration_seconds histogram"))
	assert.True(t, strings.Contains(body, "# TYPE target_app_requests_total counter"))
}

func TestMetrics_HistogramBucketsCover1msTo10s(t *testing.T) {
	srv := server.New(server.DefaultConfig())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	srv.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	assert.Contains(t, body, `le="0.001"`)
	assert.Contains(t, body, `le="10"`)
}
