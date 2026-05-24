/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Command synthetic generates deterministic traffic-series fixtures for
// the classifier and forecast-service test suites.
//
// Usage:
//
//	go run ./hack/synthetic --output=testdata --seed=42
//
// The output files are committed to testdata/ so neither Go nor Python
// test runs need to invoke this command at test time. Re-run only when
// adding new patterns or updating an existing generator.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// DataPoint is one (timestamp, rps) sample. Both Go and Python loaders
// rely on these JSON tags — do not rename without updating both sides.
type DataPoint struct {
	TimestampUnix int64   `json:"timestamp_unix"`
	RPS           float64 `json:"rps"`
}

// fixture pairs a generator with its desired point count.
type fixture struct {
	name  string
	count int
	gen   func(int64, int) []float64
}

func main() {
	outputDir := flag.String("output", "testdata", "output directory")
	seed := flag.Int64("seed", 42, "random seed for reproducibility")
	flag.Parse()

	fixtures := []fixture{
		{"flat_1440", 1440, GenFlat},
		{"periodic_1440", 1440, GenPeriodic},
		{"spiky_1440", 1440, GenSpiky},
		{"gradual_ramp_1440", 1440, GenRamp},
		{"default_1440", 1440, GenDefault},
		{"flat_70", 70, GenFlat},
		{"insufficient_50", 50, GenFlat},
	}

	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create %s: %v\n", *outputDir, err)
		os.Exit(1)
	}

	for _, f := range fixtures {
		series := f.gen(*seed, f.count)
		points := toPoints(series)
		data, err := json.MarshalIndent(points, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "marshal %s: %v\n", f.name, err)
			os.Exit(1)
		}
		path := filepath.Join(*outputDir, f.name+".json")
		if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec
			fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s (%d points)\n", path, len(points))
	}
}

// toPoints assigns 60s-spaced timestamps starting at a fixed Unix anchor
// (2024-05-23T20:53:20 UTC) so every regeneration produces byte-identical
// timestamps regardless of when the command runs.
func toPoints(series []float64) []DataPoint {
	const baseTS int64 = 1716500000
	pts := make([]DataPoint, len(series))
	for i, v := range series {
		pts[i] = DataPoint{TimestampUnix: baseTS + int64(i*60), RPS: v}
	}
	return pts
}
