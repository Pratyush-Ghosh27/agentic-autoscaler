/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package prometheus_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/prometheus"
)

func TestInstantQuery_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/query", r.URL.Path)
		assert.Equal(t, `sum(rate(http_requests_total{deployment="demo"}[2m]))`, r.URL.Query().Get("query"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "status": "success",
		  "data": {
		    "resultType": "vector",
		    "result": [
		      {"metric": {}, "value": [1716504000.000, "1234.56"]}
		    ]
		  }
		}`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 5*time.Second)
	v, err := c.InstantQuery(context.Background(), `sum(rate(http_requests_total{deployment="demo"}[2m]))`)

	require.NoError(t, err)
	assert.InDelta(t, 1234.56, v, 0.001)
}

func TestInstantQuery_NoSamplesReturnsZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 5*time.Second)
	v, err := c.InstantQuery(context.Background(), `whatever`)

	require.NoError(t, err)
	assert.Equal(t, 0.0, v, "empty result should yield zero (not an error)")
}

func TestRangeQuery_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/query_range", r.URL.Path)
		require.Equal(t, "100", r.URL.Query().Get("start"))
		require.Equal(t, "160", r.URL.Query().Get("end"))
		require.Equal(t, "60", r.URL.Query().Get("step"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "status": "success",
		  "data": {
		    "resultType": "matrix",
		    "result": [
		      {
		        "metric": {},
		        "values": [
		          [100, "10.0"],
		          [160, "12.5"]
		        ]
		      }
		    ]
		  }
		}`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 5*time.Second)
	start := time.Unix(100, 0)
	end := time.Unix(160, 0)
	samples, err := c.RangeQuery(context.Background(), "ignored", start, end, time.Minute)

	require.NoError(t, err)
	require.Len(t, samples, 2)
	assert.InDelta(t, 10.0, samples[0].Value, 0.001)
	assert.Equal(t, time.Unix(100, 0), samples[0].Timestamp)
	assert.InDelta(t, 12.5, samples[1].Value, 0.001)
}

func TestRangeQuery_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 5*time.Second)
	samples, err := c.RangeQuery(context.Background(), "x", time.Unix(0, 0), time.Unix(60, 0), time.Minute)
	require.NoError(t, err)
	assert.Empty(t, samples)
}

func TestInstantQuery_5xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 1*time.Second)
	_, err := c.InstantQuery(context.Background(), "q")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestInstantQuery_TimeoutReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 50*time.Millisecond)
	_, err := c.InstantQuery(context.Background(), "q")
	require.Error(t, err)
}

func TestInstantQuery_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not even close to json`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 1*time.Second)
	_, err := c.InstantQuery(context.Background(), "q")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

func TestRangeQuery_5xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 1*time.Second)
	_, err := c.RangeQuery(context.Background(), "q", time.Unix(0, 0), time.Unix(60, 0), time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "502")
}

func TestRangeQuery_PrometheusReportedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"error","error":"bad query"}`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 1*time.Second)
	_, err := c.RangeQuery(context.Background(), "q", time.Unix(0, 0), time.Unix(60, 0), time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad query")
}

func TestRangeQuery_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{garbage`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 1*time.Second)
	_, err := c.RangeQuery(context.Background(), "q", time.Unix(0, 0), time.Unix(60, 0), time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

func TestInstantQuery_ValueNotString(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Prometheus normally encodes the value as a string; if it ever
		// returns a number here we should surface that as an error rather
		// than silently coerce.
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
		  {"metric":{},"value":[1716504000.0, 1234.56]}
		]}}`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 1*time.Second)
	_, err := c.InstantQuery(context.Background(), "q")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a string")
}

func TestInstantQuery_ValueUnparseable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
		  {"metric":{},"value":[1716504000.0, "not-a-float"]}
		]}}`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 1*time.Second)
	_, err := c.InstantQuery(context.Background(), "q")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func TestRangeQuery_TimestampNotNumber(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
		  {"metric":{},"values":[["not-a-number","12.5"]]}
		]}}`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 1*time.Second)
	_, err := c.RangeQuery(context.Background(), "q", time.Unix(0, 0), time.Unix(60, 0), time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timestamp")
}

func TestRangeQuery_ValueNotString(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
		  {"metric":{},"values":[[100, 12.5]]}
		]}}`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 1*time.Second)
	_, err := c.RangeQuery(context.Background(), "q", time.Unix(0, 0), time.Unix(60, 0), time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a string")
}

func TestRangeQuery_UnparseableValue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "status":"success",
		  "data":{"resultType":"matrix","result":[
		    {"metric":{},"values":[[100, "not-a-float"]]}
		  ]}
		}`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 1*time.Second)
	_, err := c.RangeQuery(context.Background(), "q", time.Unix(0, 0), time.Unix(60, 0), time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func TestInstantQuery_PrometheusReportedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"error","error":"parse error","errorType":"bad_data"}`))
	}))
	defer srv.Close()

	c := prometheus.New(srv.URL, 1*time.Second)
	_, err := c.InstantQuery(context.Background(), "q")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse error")
}
