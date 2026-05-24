package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

func TestWork_HappyPathReturns200(t *testing.T) {
	cfg := server.Config{Concurrency: 1, WorkDurationMS: 10, WorkJitterMS: 0}
	srv := server.New(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/work", nil)
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"work":"done"`)
}

func TestWork_HistogramObservedAfterRequest(t *testing.T) {
	cfg := server.Config{Concurrency: 1, WorkDurationMS: 10, WorkJitterMS: 0}
	srv := server.New(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/work", nil)
	srv.Handler().ServeHTTP(rec, req)

	mrec := httptest.NewRecorder()
	mreq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	srv.Handler().ServeHTTP(mrec, mreq)

	body := mrec.Body.String()
	assert.Contains(t, body, `target_app_request_duration_seconds_count{path="/work"} 1`)
}

func TestWork_CounterIncrementedWithStatus200(t *testing.T) {
	cfg := server.Config{Concurrency: 1, WorkDurationMS: 5, WorkJitterMS: 0}
	srv := server.New(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/work", nil)
	srv.Handler().ServeHTTP(rec, req)

	mrec := httptest.NewRecorder()
	mreq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	srv.Handler().ServeHTTP(mrec, mreq)

	body := mrec.Body.String()
	assert.Contains(t, body, `target_app_requests_total{path="/work",status="200"} 1`)
}

func TestWork_BurstAboveSemaphoreLimit_Returns503(t *testing.T) {
	const concurrency = 2
	const burst = 20
	cfg := server.Config{Concurrency: concurrency, WorkDurationMS: 100, WorkJitterMS: 0}
	srv := server.New(cfg)
	handler := srv.Handler()

	var oks, rejects int32
	var wg sync.WaitGroup
	wg.Add(burst)
	for i := 0; i < burst; i++ {
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/work", nil)
			handler.ServeHTTP(rec, req)
			switch rec.Code {
			case http.StatusOK:
				atomic.AddInt32(&oks, 1)
			case http.StatusServiceUnavailable:
				atomic.AddInt32(&rejects, 1)
			}
		}()
	}
	wg.Wait()

	assert.GreaterOrEqual(t, int(oks), 1, "at least one request should succeed")
	assert.GreaterOrEqual(t, int(rejects), burst-concurrency*2, "most concurrent requests should reject")
	assert.Equal(t, burst, int(oks)+int(rejects), "every request should resolve to 200 or 503")
}

func TestWork_503CounterLabeledCorrectly(t *testing.T) {
	cfg := server.Config{Concurrency: 0, WorkDurationMS: 5, WorkJitterMS: 0}
	srv := server.New(cfg)
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/work", nil)
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	mrec := httptest.NewRecorder()
	mreq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	handler.ServeHTTP(mrec, mreq)

	body := mrec.Body.String()
	assert.Contains(t, body, `target_app_requests_total{path="/work",status="503"} 1`)
}
