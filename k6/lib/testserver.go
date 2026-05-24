/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

//go:build ignore

// testserver is a tiny HTTP server matching target-app's /work contract.
// It exists so engineers can run the k6 scenarios locally without standing
// up the full target-app + Prometheus + controller stack.
//
// Usage:
//
//	go run k6/lib/testserver.go [PORT=8080]
//
// dry-run_test.go uses an in-process httptest.Server instead.
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	port := "8080"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/work", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	fmt.Printf("test server listening on :%s\n", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil { //nolint:gosec
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
