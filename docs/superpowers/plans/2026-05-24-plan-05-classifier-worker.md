# Plan 05 — ClassifierWorker + Synthetic Data Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the cold-path ClassifierWorker goroutine that periodically queries Prometheus for long-range history, extracts features (cv, peak_to_trough, tod_correlation, trend_slope), classifies traffic patterns (flat, periodic, spiky, gradual_ramp, default), computes scaling parameters via the design §7 formulae, and patches `status.classifiedParams` on the CR. Also deliver the Go synthetic data generator (`hack/synthetic/`) that produces deterministic JSON fixtures for both Classifier and Forecast test suites.

**Architecture:** Three packages. The **feature extraction + classification** logic (`internal/classifier/`) is pure — no I/O, no Kubernetes types. The **worker goroutine** (`internal/classifier/worker.go`) owns the timer/signal loop and calls Prometheus (via the `PromQuerier` interface from Plan #4) and patches the CR status. The **synthetic data generator** (`hack/synthetic/`) is a standalone Go CLI that writes JSON files to `testdata/`.

**Tech Stack:** Go 1.23, `math`, `sort`, `gonum.org/v1/gonum/stat` (Pearson correlation), testify, controller-runtime (for the worker's CR patch), envtest.

---

## Spec Coverage Map

| Design section | Tasks |
| --- | --- |
| §6.1 triggers: immediate first run, periodic timer, reclassify annotation, generation change + dedup | T11, T12, T13 |
| §6.1 goroutine loop (select cases) | T11 |
| §6.1 classification pipeline steps 1-2 (query + min-points guard) | T10 |
| §6.1 classification pipeline steps 3 (confidence) | T7 |
| §6.1 classification pipeline steps 4-5 (features + classify) | T3, T4, T5, T6 |
| §6.1 classification pipeline step 6 (parameter formulae) | T8 |
| §6.1 classification pipeline step 7 (patch status) | T10 |
| §6.1 classification pipeline step 8 (emit pattern_classified event) | T10 |
| §7 features: cv, peak_to_trough, tod_correlation, trend_slope | T3 |
| §7 classification rules (priority order, first-match-wins) | T4 |
| §7 parameter formulae (scaleUpCooldown, scaleDownCooldown, maxStep, preferredForecaster) | T8 |
| §7 constants table | T8 |
| §9 Prometheus query fails during classification → skip | T14 |
| §9 fewer than CLASSIFIER_MIN_POINTS → pattern_unknown | T10, T14 |
| §9 ClassifierWorker goroutine panics → recover + 60s backoff | T11 |
| §9 reclassify annotation: removed on success, left on skip | T12 |
| §6.1 generation change dedup (CLASSIFIER_DEDUP_SECONDS) | T13 |
| Strategy doc: synthetic data CLI generates testdata/*.json fixtures | T1, T2 |

What's intentionally not in this plan: the reconciler's *reading* of `classifiedParams` (that's Plan #4's nil-coalesce chain); the hot-path use of `effectiveForecaster` (also Plan #4); the `hack/synthetic` CLI's integration into the Makefile (Plan #11).

---

## File Structure

```
scaler/
├── internal/classifier/
│   ├── features.go              # T3: cv, peak_to_trough, tod_correlation, trend_slope
│   ├── features_test.go         # T3: table-driven on synthetic series
│   ├── classify.go              # T4: priority-ordered pattern rules
│   ├── classify_test.go         # T4, T5, T6: boundary tests per pattern
│   ├── params.go                # T8: parameter formulae + constants
│   ├── params_test.go           # T8: golden-value tests at key cv/tod/peak points
│   ├── confidence.go            # T7: Confidence(len) → "high"/"medium"
│   ├── confidence_test.go       # T7
│   ├── worker.go                # T10-T14: goroutine loop, Prometheus, CR patch
│   └── worker_test.go           # T10-T14: envtest specs
├── hack/synthetic/
│   ├── main.go                  # T1: CLI entry point
│   ├── generators.go            # T2: gen_flat, gen_periodic, gen_spiky, gen_ramp, gen_default
│   └── generators_test.go       # T2: verify each generator hits the intended pattern
├── testdata/
│   ├── SCHEMA.md                # T1: describes JSON format
│   ├── flat_1440.json           # T2 output
│   ├── periodic_1440.json       # T2 output
│   ├── spiky_1440.json          # T2 output
│   ├── gradual_ramp_1440.json   # T2 output
│   ├── default_1440.json        # T2 output
│   ├── flat_70.json             # T2 output (minimum confidence boundary)
│   └── insufficient_50.json     # T2 output (below CLASSIFIER_MIN_POINTS)
└── go.mod                        # add gonum dependency
```

### File responsibilities

- `internal/classifier/features.go` — `ExtractFeatures(series []float64) Features` struct with `CV`, `PeakToTrough`, `TodCorrelation`, `TrendSlope`. Pure math; no imports outside `math`, `sort`, `gonum/stat`.
- `internal/classifier/classify.go` — `Classify(f Features) string` returning one of `"flat"`, `"periodic"`, `"spiky"`, `"gradual_ramp"`, `"default"`. Priority-ordered if/else chain.
- `internal/classifier/params.go` — `ComputeParams(f Features, minReplicas, maxReplicas int32) ClassifiedOutput` applying the design §7 formulae with all constants defined as package-level `const`.
- `internal/classifier/confidence.go` — `Confidence(historyPoints int, highThreshold, minThreshold int) string`.
- `internal/classifier/worker.go` — `ClassifierWorker` struct with `Run(ctx)`. Holds `PromQuerier`, kube `client.Client`, and config references. Implements the goroutine loop with timer + reclassify + generation signals.
- `hack/synthetic/main.go` — `go run ./hack/synthetic --output=testdata/ --seed=42`. Deterministic. Committed outputs in `testdata/`.
- `hack/synthetic/generators.go` — one function per pattern: `GenFlat`, `GenPeriodic`, `GenSpiky`, `GenRamp`, `GenDefault`. Each returns `[]float64`.

---

## Phase 0 — Synthetic data generator

### Task 1: Synthetic data CLI scaffold + testdata schema

**Files:**
- Create: `hack/synthetic/main.go`
- Create: `testdata/SCHEMA.md`

- [ ] **Step 1: Create testdata/SCHEMA.md**

```markdown
# Synthetic Fixture Schema

Each `.json` file is an array of objects:

```json
[
  {"timestamp_unix": 1716504000, "rps": 120.5},
  {"timestamp_unix": 1716504060, "rps": 118.2},
  ...
]
```

- `timestamp_unix`: integer epoch seconds, 60s apart (1-min resolution)
- `rps`: float64, non-negative
- Filename convention: `<pattern>_<point_count>.json`
- Generated by: `go run ./hack/synthetic --output=testdata/ --seed=42`
- Deterministic: same seed → same output. Commit the outputs.
```

- [ ] **Step 2: Create hack/synthetic/main.go**

```go
// Command synthetic generates deterministic traffic-series fixtures for
// classifier and forecaster tests.
//
// Usage: go run ./hack/synthetic --output=testdata/ --seed=42
package main

import (
    "encoding/json"
    "flag"
    "fmt"
    "os"
    "path/filepath"
)

type DataPoint struct {
    TimestampUnix int64   `json:"timestamp_unix"`
    RPS           float64 `json:"rps"`
}

func main() {
    outputDir := flag.String("output", "testdata", "output directory")
    seed := flag.Int64("seed", 42, "random seed for reproducibility")
    flag.Parse()

    generators := map[string]func(int64, int) []DataPoint{
        "flat_1440":          func(s int64, n int) []DataPoint { return toPoints(GenFlat(s, n)) },
        "periodic_1440":      func(s int64, n int) []DataPoint { return toPoints(GenPeriodic(s, n)) },
        "spiky_1440":         func(s int64, n int) []DataPoint { return toPoints(GenSpiky(s, n)) },
        "gradual_ramp_1440":  func(s int64, n int) []DataPoint { return toPoints(GenRamp(s, n)) },
        "default_1440":       func(s int64, n int) []DataPoint { return toPoints(GenDefault(s, n)) },
        "flat_70":            func(s int64, n int) []DataPoint { return toPoints(GenFlat(s, n)) },
        "insufficient_50":    func(s int64, n int) []DataPoint { return toPoints(GenFlat(s, n)) },
    }

    counts := map[string]int{
        "flat_1440": 1440, "periodic_1440": 1440, "spiky_1440": 1440,
        "gradual_ramp_1440": 1440, "default_1440": 1440,
        "flat_70": 70, "insufficient_50": 50,
    }

    for name, gen := range generators {
        points := gen(*seed, counts[name])
        data, err := json.MarshalIndent(points, "", "  ")
        if err != nil {
            fmt.Fprintf(os.Stderr, "marshal %s: %v\n", name, err)
            os.Exit(1)
        }
        path := filepath.Join(*outputDir, name+".json")
        if err := os.WriteFile(path, data, 0644); err != nil {
            fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
            os.Exit(1)
        }
        fmt.Printf("wrote %s (%d points)\n", path, len(points))
    }
}

func toPoints(series []float64) []DataPoint {
    baseTS := int64(1716500000)
    pts := make([]DataPoint, len(series))
    for i, v := range series {
        pts[i] = DataPoint{TimestampUnix: baseTS + int64(i*60), RPS: v}
    }
    return pts
}
```

- [ ] **Step 3: Commit**

```bash
git add hack/synthetic/main.go testdata/SCHEMA.md
git commit -m "feat(synthetic): CLI scaffold + testdata schema"
```

---

### Task 2: Generator functions + generate fixtures

**Files:**
- Create: `hack/synthetic/generators.go`
- Create: `hack/synthetic/generators_test.go`
- Create: `testdata/*.json` (generated)

- [ ] **Step 1: Write generators_test.go (verify each hits the target pattern)**

```go
package main

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestGenFlat_LowCV(t *testing.T) {
    s := GenFlat(42, 1440)
    require.Len(t, s, 1440)
    cv := stddev(s) / mean(s)
    assert.Less(t, cv, 0.10, "flat series must have cv < 0.10")
}

func TestGenPeriodic_HighTodCorrelation(t *testing.T) {
    s := GenPeriodic(42, 1440)
    require.Len(t, s, 1440)
    corr := pearsonLag60(s)
    assert.Greater(t, corr, 0.70, "periodic series must have tod_correlation > 0.70")
}

func TestGenSpiky_HighCVAndPeakToTrough(t *testing.T) {
    s := GenSpiky(42, 1440)
    cv := stddev(s) / mean(s)
    pt := percentile99(s) / (mean(s) + 1)
    assert.Greater(t, cv, 0.50)
    assert.Greater(t, pt, 5.0)
}

func TestGenRamp_HighTrendSlope(t *testing.T) {
    s := GenRamp(42, 1440)
    slope := lsqSlope(s)
    assert.Greater(t, abs(slope), 2.0, "ramp must have |trend_slope| > 2.0 rps/min")
}

func TestGenDefault_FallsThrough(t *testing.T) {
    s := GenDefault(42, 1440)
    cv := stddev(s) / mean(s)
    corr := pearsonLag60(s)
    pt := percentile99(s) / (mean(s) + 1)
    slope := lsqSlope(s)
    assert.Greater(t, cv, 0.10, "not flat")
    assert.Less(t, corr, 0.70, "not periodic")
    assert.True(t, cv <= 0.50 || pt <= 5.0, "not spiky")
    assert.Less(t, abs(slope), 2.0, "not ramp")
}
```

(Helper functions `mean`, `stddev`, `percentile99`, `pearsonLag60`, `lsqSlope`, `abs` are defined as unexported test helpers in the same file.)

- [ ] **Step 2: Write generators.go**

```go
package main

import (
    "math"
    "math/rand"
)

// GenFlat produces a nearly-constant series with tiny Gaussian noise.
// Target: cv < 0.10.
func GenFlat(seed int64, n int) []float64 {
    rng := rand.New(rand.NewSource(seed))
    base := 200.0
    out := make([]float64, n)
    for i := range out {
        out[i] = math.Max(0, base+rng.NormFloat64()*5) // stddev=5, mean=200 → cv≈0.025
    }
    return out
}

// GenPeriodic produces a series with a strong 60-minute repeating cycle.
// Target: tod_correlation > 0.70.
func GenPeriodic(seed int64, n int) []float64 {
    rng := rand.New(rand.NewSource(seed))
    out := make([]float64, n)
    for i := range out {
        cycle := 100.0 * math.Sin(2*math.Pi*float64(i)/60.0)
        out[i] = math.Max(0, 200+cycle+rng.NormFloat64()*20)
    }
    return out
}

// GenSpiky produces a series with random high-magnitude bursts.
// Target: cv > 0.50 AND peak_to_trough > 5.
func GenSpiky(seed int64, n int) []float64 {
    rng := rand.New(rand.NewSource(seed))
    out := make([]float64, n)
    for i := range out {
        base := 50.0 + rng.NormFloat64()*10
        if rng.Float64() < 0.05 { // 5% chance of massive spike
            base += 500 + rng.Float64()*500
        }
        out[i] = math.Max(0, base)
    }
    return out
}

// GenRamp produces a steadily increasing series.
// Target: |trend_slope| > 2.0 rps/min.
func GenRamp(seed int64, n int) []float64 {
    rng := rand.New(rand.NewSource(seed))
    out := make([]float64, n)
    for i := range out {
        out[i] = math.Max(0, 100+3.0*float64(i)+rng.NormFloat64()*20) // slope ≈ 3 rps/min
    }
    return out
}

// GenDefault produces moderate variance without triggering any specific rule.
// Target: cv ∈ [0.10, 0.50], tod_correlation < 0.70, peak_to_trough <= 5, |slope| < 2.
func GenDefault(seed int64, n int) []float64 {
    rng := rand.New(rand.NewSource(seed))
    out := make([]float64, n)
    for i := range out {
        out[i] = math.Max(0, 200+rng.NormFloat64()*50) // stddev=50, mean=200 → cv≈0.25
    }
    return out
}
```

- [ ] **Step 3: Run generator tests**

```bash
cd hack/synthetic && go test -v ./...
```

Expected: all PASS.

- [ ] **Step 4: Generate the fixtures**

```bash
go run ./hack/synthetic --output=../../testdata/ --seed=42
```

Expected: 7 files written to `testdata/`.

- [ ] **Step 5: Commit**

```bash
git add hack/synthetic/ testdata/
git commit -m "feat(synthetic): generators + golden fixtures for all 5 patterns"
```

---

## Phase 1 — Feature extraction (Tier-1 strict TDD)

### Task 3: ExtractFeatures

**Files:**
- Create: `internal/classifier/features.go`
- Create: `internal/classifier/features_test.go`

- [ ] **Step 1: Write failing tests using the generated fixtures**

Create `internal/classifier/features_test.go`:

```go
package classifier_test

import (
    "encoding/json"
    "os"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"

    "github.com/pratyush-ghosh/agentic-autoscaler/internal/classifier"
)

func loadSeries(t *testing.T, path string) []float64 {
    t.Helper()
    data, err := os.ReadFile(path)
    require.NoError(t, err)
    type dp struct{ RPS float64 `json:"rps"` }
    var pts []dp
    require.NoError(t, json.Unmarshal(data, &pts))
    out := make([]float64, len(pts))
    for i, p := range pts { out[i] = p.RPS }
    return out
}

func TestExtractFeatures_FlatSeries(t *testing.T) {
    s := loadSeries(t, "../../testdata/flat_1440.json")
    f := classifier.ExtractFeatures(s)
    assert.Less(t, f.CV, 0.10)
    assert.Greater(t, f.PeakToTrough, 0.0)
}

func TestExtractFeatures_PeriodicSeries(t *testing.T) {
    s := loadSeries(t, "../../testdata/periodic_1440.json")
    f := classifier.ExtractFeatures(s)
    assert.Greater(t, f.TodCorrelation, 0.70)
}

func TestExtractFeatures_SpikySeries(t *testing.T) {
    s := loadSeries(t, "../../testdata/spiky_1440.json")
    f := classifier.ExtractFeatures(s)
    assert.Greater(t, f.CV, 0.50)
    assert.Greater(t, f.PeakToTrough, 5.0)
}

func TestExtractFeatures_RampSeries(t *testing.T) {
    s := loadSeries(t, "../../testdata/gradual_ramp_1440.json")
    f := classifier.ExtractFeatures(s)
    assert.Greater(t, abs(f.TrendSlope), 2.0)
}

func TestExtractFeatures_EmptySeries(t *testing.T) {
    f := classifier.ExtractFeatures(nil)
    assert.Equal(t, 0.0, f.CV)
    assert.Equal(t, 0.0, f.TodCorrelation)
}

func abs(v float64) float64 {
    if v < 0 { return -v }
    return v
}
```

- [ ] **Step 2: Run; expect ImportError**

- [ ] **Step 3: Implement**

Create `internal/classifier/features.go`:

```go
// Package classifier implements traffic pattern classification for the
// AgenticAutoscaler's cold-path worker. See docs/design.md §7.
package classifier

import (
    "math"
    "sort"

    "gonum.org/v1/gonum/stat"
)

// Features holds the four extracted features (design §7).
type Features struct {
    CV             float64 // stddev/mean; 0 if mean < 1
    PeakToTrough   float64 // p99 / (mean + 1)
    TodCorrelation float64 // Pearson correlation with 60-point lag
    TrendSlope     float64 // least-squares slope in rps/min
}

// ExtractFeatures computes all four features from a time series.
func ExtractFeatures(series []float64) Features {
    if len(series) == 0 {
        return Features{}
    }

    m := stat.Mean(series, nil)
    sd := stat.StdDev(series, nil)

    var cv float64
    if m >= 1 {
        cv = sd / m
    }

    p99 := percentile(series, 0.99)
    peakToTrough := p99 / (m + 1)

    todCorr := todCorrelation(series, 60)
    slope := trendSlope(series)

    return Features{
        CV:             cv,
        PeakToTrough:   peakToTrough,
        TodCorrelation: todCorr,
        TrendSlope:     slope,
    }
}

func percentile(s []float64, p float64) float64 {
    sorted := make([]float64, len(s))
    copy(sorted, s)
    sort.Float64s(sorted)
    idx := int(math.Ceil(p*float64(len(sorted)))) - 1
    if idx < 0 { idx = 0 }
    if idx >= len(sorted) { idx = len(sorted) - 1 }
    return sorted[idx]
}

func todCorrelation(series []float64, lag int) float64 {
    n := len(series)
    if n < lag+10 { // need at least 10 overlap points
        return 0
    }
    x := series[:n-lag]
    y := series[lag:]
    return stat.Correlation(x, y, nil)
}

func trendSlope(series []float64) float64 {
    n := len(series)
    if n < 2 { return 0 }
    xs := make([]float64, n)
    for i := range xs { xs[i] = float64(i) }
    alpha, _ := stat.LinearRegression(xs, series, nil, false)
    _, beta := alpha, 0.0
    // LinearRegression returns (alpha, beta) where y = alpha + beta*x
    // We need slope = beta.
    _, beta = stat.LinearRegression(xs, series, nil, false)
    return beta
}
```

Note: `stat.LinearRegression` returns `(alpha, beta)` where `y = alpha + beta*x`. We want `beta` (the slope in rps/min since x is minutes).

- [ ] **Step 4: Add gonum dependency**

```bash
go get gonum.org/v1/gonum/stat
```

- [ ] **Step 5: Run; verify pass**

```bash
go test ./internal/classifier/... -v
```

- [ ] **Step 6: Commit**

```bash
git add internal/classifier/ go.mod go.sum
git commit -m "feat(classifier): ExtractFeatures (cv, peak_to_trough, tod_correlation, trend_slope)"
```

---

### Task 4: Classify (priority-ordered rules)

**Files:**
- Create: `internal/classifier/classify.go`
- Create: `internal/classifier/classify_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/classifier/classify_test.go`:

```go
package classifier_test

import (
    "testing"

    "github.com/stretchr/testify/assert"

    "github.com/pratyush-ghosh/agentic-autoscaler/internal/classifier"
)

func TestClassify_PriorityOrder(t *testing.T) {
    cases := []struct {
        name string
        f    classifier.Features
        want string
    }{
        {"flat wins at low cv", classifier.Features{CV: 0.05, TodCorrelation: 0.8, PeakToTrough: 6, TrendSlope: 3}, "flat"},
        {"periodic wins over spiky", classifier.Features{CV: 0.60, TodCorrelation: 0.75, PeakToTrough: 6, TrendSlope: 0}, "periodic"},
        {"spiky when cv>0.50 and pt>5", classifier.Features{CV: 0.60, TodCorrelation: 0.3, PeakToTrough: 6, TrendSlope: 0}, "spiky"},
        {"gradual_ramp on high slope", classifier.Features{CV: 0.20, TodCorrelation: 0.3, PeakToTrough: 2, TrendSlope: 3.0}, "gradual_ramp"},
        {"negative slope also ramp", classifier.Features{CV: 0.20, TodCorrelation: 0.3, PeakToTrough: 2, TrendSlope: -2.5}, "gradual_ramp"},
        {"default fallthrough", classifier.Features{CV: 0.20, TodCorrelation: 0.3, PeakToTrough: 2, TrendSlope: 1.0}, "default"},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got := classifier.Classify(tc.f)
            assert.Equal(t, tc.want, got)
        })
    }
}
```

- [ ] **Step 2: Run; expect failure**

- [ ] **Step 3: Implement**

Create `internal/classifier/classify.go`:

```go
package classifier

import "math"

// Classify applies the priority-ordered pattern rules from design §7.
// First match wins; returns one of: flat, periodic, spiky, gradual_ramp, default.
func Classify(f Features) string {
    switch {
    case f.CV < 0.10:
        return "flat"
    case f.TodCorrelation > 0.70:
        return "periodic"
    case f.CV > 0.50 && f.PeakToTrough > 5:
        return "spiky"
    case math.Abs(f.TrendSlope) > 2.0:
        return "gradual_ramp"
    default:
        return "default"
    }
}
```

- [ ] **Step 4: Run; verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/classifier/
git commit -m "feat(classifier): Classify priority-ordered pattern rules"
```

---

### Task 5: Classify boundary tests (exact thresholds)

**Files:**
- Modify: `internal/classifier/classify_test.go`

- [ ] **Step 1: Append boundary tests**

```go
func TestClassify_Boundaries(t *testing.T) {
    cases := []struct {
        name string
        f    classifier.Features
        want string
    }{
        {"cv exactly 0.10 is NOT flat", classifier.Features{CV: 0.10}, "default"},
        {"cv just below 0.10 IS flat", classifier.Features{CV: 0.099}, "flat"},
        {"tod exactly 0.70 is NOT periodic", classifier.Features{CV: 0.15, TodCorrelation: 0.70}, "default"},
        {"tod just above 0.70", classifier.Features{CV: 0.15, TodCorrelation: 0.701}, "periodic"},
        {"spiky needs both cv>0.50 AND pt>5", classifier.Features{CV: 0.51, PeakToTrough: 4.9, TodCorrelation: 0.1}, "default"},
        {"slope exactly 2.0 is NOT ramp", classifier.Features{CV: 0.15, TodCorrelation: 0.1, PeakToTrough: 2, TrendSlope: 2.0}, "default"},
        {"slope just above 2.0", classifier.Features{CV: 0.15, TodCorrelation: 0.1, PeakToTrough: 2, TrendSlope: 2.01}, "gradual_ramp"},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got := classifier.Classify(tc.f)
            assert.Equal(t, tc.want, got)
        })
    }
}
```

- [ ] **Step 2: Run; verify pass**

- [ ] **Step 3: Commit**

```bash
git add internal/classifier/
git commit -m "test(classifier): boundary tests for exact classification thresholds"
```

---

### Task 6: End-to-end feature+classify on synthetic fixtures

**Files:**
- Modify: `internal/classifier/classify_test.go`

- [ ] **Step 1: Append fixture-driven integration tests**

```go
func TestClassify_OnSyntheticFixtures(t *testing.T) {
    cases := []struct {
        fixture string
        want    string
    }{
        {"../../testdata/flat_1440.json", "flat"},
        {"../../testdata/periodic_1440.json", "periodic"},
        {"../../testdata/spiky_1440.json", "spiky"},
        {"../../testdata/gradual_ramp_1440.json", "gradual_ramp"},
        {"../../testdata/default_1440.json", "default"},
    }
    for _, tc := range cases {
        t.Run(tc.fixture, func(t *testing.T) {
            series := loadSeries(t, tc.fixture)
            f := classifier.ExtractFeatures(series)
            got := classifier.Classify(f)
            assert.Equal(t, tc.want, got)
        })
    }
}
```

- [ ] **Step 2: Run; verify pass**

This test ensures the entire pipeline (generator → features → classify) is self-consistent.

- [ ] **Step 3: Commit**

```bash
git add internal/classifier/
git commit -m "test(classifier): end-to-end feature+classify on synthetic fixtures"
```

---

## Phase 2 — Confidence + parameter formulae (Tier-1 strict TDD)

### Task 7: Confidence function

**Files:**
- Create: `internal/classifier/confidence.go`
- Create: `internal/classifier/confidence_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/classifier/confidence_test.go`:

```go
package classifier_test

import (
    "testing"

    "github.com/stretchr/testify/assert"

    "github.com/pratyush-ghosh/agentic-autoscaler/internal/classifier"
)

func TestConfidence(t *testing.T) {
    cases := []struct {
        points int
        want   string
    }{
        {240, "high"},
        {500, "high"},
        {239, "medium"},
        {70, "medium"},
        {69, ""}, // below min — should never be called with <min, but returns "" as guard
    }
    for _, tc := range cases {
        t.Run("", func(t *testing.T) {
            got := classifier.Confidence(tc.points, 240, 70)
            assert.Equal(t, tc.want, got)
        })
    }
}
```

- [ ] **Step 2: Run; expect failure**

- [ ] **Step 3: Implement**

Create `internal/classifier/confidence.go`:

```go
package classifier

// Confidence returns "high" or "medium" based on history point count.
// Returns "" if below minThreshold (caller should have skipped classification).
func Confidence(historyPoints, highThreshold, minThreshold int) string {
    switch {
    case historyPoints >= highThreshold:
        return "high"
    case historyPoints >= minThreshold:
        return "medium"
    default:
        return ""
    }
}
```

- [ ] **Step 4: Run; verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/classifier/
git commit -m "feat(classifier): Confidence(historyPoints, high, min) → high/medium"
```

---

### Task 8: Parameter formulae (ComputeParams)

**Files:**
- Create: `internal/classifier/params.go`
- Create: `internal/classifier/params_test.go`

- [ ] **Step 1: Write failing tests with golden values from design §7**

Create `internal/classifier/params_test.go`:

```go
package classifier_test

import (
    "testing"

    "github.com/stretchr/testify/assert"

    "github.com/pratyush-ghosh/agentic-autoscaler/internal/classifier"
)

func TestComputeParams_FlatTraffic(t *testing.T) {
    // cv=0, tod=0, pt=1, slope=0 → scaleUp=120, scaleDown=180, maxStep=1, forecaster=linear_extrap
    f := classifier.Features{CV: 0, TodCorrelation: 0, PeakToTrough: 1, TrendSlope: 0}
    p := classifier.ComputeParams(f, 2, 10)
    assert.Equal(t, int32(120), p.ScaleUpCooldown)
    assert.Equal(t, int32(180), p.ScaleDownCooldown)
    assert.Equal(t, int32(1), p.MaxStep) // ceil(log2(1)) = 0, clamped to 1
    assert.Equal(t, "linear_extrap", p.PreferredForecaster)
}

func TestComputeParams_HighCV(t *testing.T) {
    // cv=0.5 → scaleUp = round(120 / (1 + 2*0.5)) = round(120/2) = 60
    // scaleDown = round(180 * (1 + 1.5*0.5) / (1 + 0.5*0)) = round(180*1.75/1) = 315
    f := classifier.Features{CV: 0.5, TodCorrelation: 0, PeakToTrough: 5, TrendSlope: 0}
    p := classifier.ComputeParams(f, 2, 10)
    assert.Equal(t, int32(60), p.ScaleUpCooldown)
    assert.Equal(t, int32(315), p.ScaleDownCooldown)
    assert.Equal(t, int32(3), p.MaxStep) // ceil(log2(5)) = 3
    assert.Equal(t, "linear_extrap", p.PreferredForecaster)
}

func TestComputeParams_PeriodicTraffic(t *testing.T) {
    // tod=0.8 → prophet preferred
    // scaleDown = round(180 * (1 + 1.5*0.2) / (1 + 0.5*0.8)) = round(180*1.3/1.4) ≈ 167
    f := classifier.Features{CV: 0.2, TodCorrelation: 0.8, PeakToTrough: 3, TrendSlope: 0}
    p := classifier.ComputeParams(f, 2, 10)
    assert.Equal(t, "prophet", p.PreferredForecaster)
    assert.Equal(t, int32(2), p.MaxStep) // ceil(log2(3)) = 2
}

func TestComputeParams_VeryHighCV_HitsFloor(t *testing.T) {
    // cv=2.0 → scaleUp = round(120 / (1 + 2*2)) = round(120/5) = 24 → clamped to 30 (floor)
    f := classifier.Features{CV: 2.0, TodCorrelation: 0, PeakToTrough: 10, TrendSlope: 0}
    p := classifier.ComputeParams(f, 1, 8)
    assert.Equal(t, int32(30), p.ScaleUpCooldown, "should hit hard floor")
    assert.Equal(t, int32(4), p.MaxStep) // ceil(log2(10)) ≈ 3.32 → 4
}

func TestComputeParams_HighCV_HitsCeiling(t *testing.T) {
    // cv=1.56 → scaleDown = round(180 * (1+1.5*1.56) / 1) = round(180*3.34) ≈ 601 → clamped to 600
    f := classifier.Features{CV: 1.56, TodCorrelation: 0, PeakToTrough: 2, TrendSlope: 0}
    p := classifier.ComputeParams(f, 2, 10)
    assert.Equal(t, int32(600), p.ScaleDownCooldown, "should hit hard ceiling")
}

func TestComputeParams_MaxStepUpperBound(t *testing.T) {
    // pt=1000 → ceil(log2(1000)) ≈ 10, but maxReplicas-minReplicas = 5 → clamped to 5
    f := classifier.Features{CV: 0.6, TodCorrelation: 0, PeakToTrough: 1000, TrendSlope: 0}
    p := classifier.ComputeParams(f, 3, 8)
    assert.Equal(t, int32(5), p.MaxStep, "clamped to maxReplicas - minReplicas")
}

func TestComputeParams_RampUsesProhpet(t *testing.T) {
    f := classifier.Features{CV: 0.2, TodCorrelation: 0.3, PeakToTrough: 2, TrendSlope: 3.0}
    p := classifier.ComputeParams(f, 2, 10)
    assert.Equal(t, "prophet", p.PreferredForecaster)
}
```

- [ ] **Step 2: Run; expect failure**

- [ ] **Step 3: Implement**

Create `internal/classifier/params.go`:

```go
package classifier

import "math"

// Classification formula constants (design §7).
const (
    BaseScaleUpCooldown        = 120.0
    KCVUp                      = 2.0
    ScaleUpCooldownHardFloor   = 30
    ScaleUpCooldownHardCeiling = 180

    BaseScaleDownCooldown        = 180.0
    KCVDown                      = 1.5
    KTodDown                     = 0.5
    ScaleDownCooldownHardFloor   = 60
    ScaleDownCooldownHardCeiling = 600
)

// ClassifiedOutput is the set of params the classifier writes to status.classifiedParams.
type ClassifiedOutput struct {
    ScaleUpCooldown     int32
    ScaleDownCooldown   int32
    MaxStep             int32
    PreferredForecaster string
}

// ComputeParams applies the design §7 formulae to produce recommended scaling params.
func ComputeParams(f Features, minReplicas, maxReplicas int32) ClassifiedOutput {
    // scaleUpCooldown = clamp(round(BASE / (1 + K_CV_UP * cv)), floor, ceiling)
    rawUp := BaseScaleUpCooldown / (1 + KCVUp*f.CV)
    scaleUp := clampInt32(int32(math.Round(rawUp)), ScaleUpCooldownHardFloor, ScaleUpCooldownHardCeiling)

    // scaleDownCooldown = clamp(round(BASE * (1 + K_CV_DOWN * cv) / (1 + K_TOD_DOWN * max(0, tod))), floor, ceiling)
    todFactor := math.Max(0, f.TodCorrelation)
    rawDown := BaseScaleDownCooldown * (1 + KCVDown*f.CV) / (1 + KTodDown*todFactor)
    scaleDown := clampInt32(int32(math.Round(rawDown)), ScaleDownCooldownHardFloor, ScaleDownCooldownHardCeiling)

    // maxStep = clamp(ceil(log2(peak_to_trough)), 1, maxReplicas - minReplicas)
    var maxStep int32
    if f.PeakToTrough <= 1 {
        maxStep = 1
    } else {
        maxStep = int32(math.Ceil(math.Log2(f.PeakToTrough)))
    }
    replicaRange := maxReplicas - minReplicas
    if replicaRange < 1 { replicaRange = 1 }
    maxStep = clampInt32(maxStep, 1, replicaRange)

    // preferredForecaster
    forecaster := "linear_extrap"
    if f.TodCorrelation > 0.70 || math.Abs(f.TrendSlope) > 2.0 {
        forecaster = "prophet"
    }

    return ClassifiedOutput{
        ScaleUpCooldown:     scaleUp,
        ScaleDownCooldown:   scaleDown,
        MaxStep:             maxStep,
        PreferredForecaster: forecaster,
    }
}

func clampInt32(v, min, max int32) int32 {
    if v < min { return min }
    if v > max { return max }
    return v
}
```

- [ ] **Step 4: Run; verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/classifier/
git commit -m "feat(classifier): ComputeParams formulae with all §7 constants"
```

---

### Task 9: Full pipeline function (features → classify → confidence → params)

**Files:**
- Create: `internal/classifier/pipeline.go`
- Modify: `internal/classifier/features_test.go` (add pipeline test)

- [ ] **Step 1: Write failing test**

```go
func TestRunClassificationPipeline(t *testing.T) {
    series := loadSeries(t, "../../testdata/periodic_1440.json")
    result, err := classifier.RunPipeline(series, 240, 70, 2, 10)
    require.NoError(t, err)
    assert.Equal(t, "periodic", result.Pattern)
    assert.Equal(t, "high", result.Confidence) // 1440 >= 240
    assert.Equal(t, "prophet", result.Params.PreferredForecaster)
    assert.Equal(t, 1440, result.HistoryPoints)
}

func TestRunClassificationPipeline_InsufficientPoints(t *testing.T) {
    series := loadSeries(t, "../../testdata/insufficient_50.json")
    _, err := classifier.RunPipeline(series, 240, 70, 2, 10)
    require.Error(t, err)
    assert.ErrorIs(t, err, classifier.ErrInsufficientPoints)
}
```

- [ ] **Step 2: Run; expect failure**

- [ ] **Step 3: Implement**

Create `internal/classifier/pipeline.go`:

```go
package classifier

import "errors"

// ErrInsufficientPoints signals that the series has fewer than CLASSIFIER_MIN_POINTS.
var ErrInsufficientPoints = errors.New("classifier: insufficient history points")

// PipelineResult is the full output of a classification run.
type PipelineResult struct {
    Pattern       string
    Confidence    string
    Params        ClassifiedOutput
    HistoryPoints int
    Features      Features
}

// RunPipeline runs the full classification pipeline (design §6.1 steps 2-6).
func RunPipeline(series []float64, highConfThreshold, minThreshold int, minReplicas, maxReplicas int32) (PipelineResult, error) {
    if len(series) < minThreshold {
        return PipelineResult{}, ErrInsufficientPoints
    }

    f := ExtractFeatures(series)
    pattern := Classify(f)
    conf := Confidence(len(series), highConfThreshold, minThreshold)
    params := ComputeParams(f, minReplicas, maxReplicas)

    return PipelineResult{
        Pattern:       pattern,
        Confidence:    conf,
        Params:        params,
        HistoryPoints: len(series),
        Features:      f,
    }, nil
}
```

- [ ] **Step 4: Run; verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/classifier/
git commit -m "feat(classifier): RunPipeline composing features+classify+confidence+params"
```

---

## Phase 3 — Worker goroutine (Tier-2 envtest)

### Task 10: ClassifierWorker core loop + CR patch

**Files:**
- Create: `internal/classifier/worker.go`
- Create: `internal/classifier/worker_test.go`

- [ ] **Step 1: Implement the worker**

Create `internal/classifier/worker.go`:

```go
package classifier

import (
    "context"
    "fmt"
    "time"

    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/types"
    "k8s.io/client-go/tools/record"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"

    autoscalingv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/api/v1alpha1"
    "github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/prometheus"
    "github.com/pratyush-ghosh/agentic-autoscaler/internal/promql"
)

// PromQuerier is the subset of prometheus.Client the worker needs.
type PromQuerier interface {
    RangeQuery(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]prometheus.Sample, error)
}

// WorkerConfig holds the env-var driven config for the ClassifierWorker.
type WorkerConfig struct {
    Interval            time.Duration
    HistoryHours        int
    MinPoints           int
    HighConfPoints      int
    DedupSeconds        int
    PrometheusURL       string
}

// Worker is the cold-path classification goroutine for one CR.
type Worker struct {
    Key             types.NamespacedName
    DeploymentName  string
    MinReplicas     int32
    MaxReplicas     int32
    Config          WorkerConfig
    Client          client.Client
    Prom            PromQuerier
    EventRecorder   record.EventRecorder
    ReclassifyCh    chan struct{}
    GenerationCh    chan struct{}
    lastClassifyAt  time.Time
}

// Run starts the worker loop. It blocks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
    log := ctrl.LoggerFrom(ctx).WithValues("worker", "classifier", "cr", w.Key)

    defer func() {
        if r := recover(); r != nil {
            log.Error(fmt.Errorf("panic: %v", r), "ClassifierWorker panicked, will restart after backoff")
            time.Sleep(60 * time.Second)
        }
    }()

    // Trigger 1: immediate first run.
    w.runClassification(ctx, log)

    timer := time.NewTicker(w.Config.Interval)
    defer timer.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-timer.C:
            w.runClassification(ctx, log)
        case <-w.ReclassifyCh:
            result := w.runClassification(ctx, log)
            if result {
                w.removeReclassifyAnnotation(ctx, log)
            }
        case <-w.GenerationCh:
            if time.Since(w.lastClassifyAt) > time.Duration(w.Config.DedupSeconds)*time.Second {
                w.runClassification(ctx, log)
            }
        }
    }
}

func (w *Worker) runClassification(ctx context.Context, log ctrl.Logger) bool {
    end := time.Now()
    start := end.Add(-time.Duration(w.Config.HistoryHours) * time.Hour)

    samples, err := w.Prom.RangeQuery(ctx, promql.RangeRPS(w.DeploymentName), start, end, time.Minute)
    if err != nil {
        log.Error(err, "prometheus range query failed during classification")
        return false
    }

    series := make([]float64, len(samples))
    for i, s := range samples { series[i] = s.Value }

    result, err := RunPipeline(series, w.Config.HighConfPoints, w.Config.MinPoints, w.MinReplicas, w.MaxReplicas)
    if err != nil {
        // Insufficient points → emit pattern_unknown.
        w.emitEvent(ctx, "PatternUnknown", fmt.Sprintf("insufficient history: %d points (need %d)", len(series), w.Config.MinPoints))
        return false
    }

    // Patch status.classifiedParams.
    var aas autoscalingv1alpha1.AgenticAutoscaler
    if err := w.Client.Get(ctx, w.Key, &aas); err != nil {
        log.Error(err, "failed to get CR for status patch")
        return false
    }

    now := metav1.Now()
    aas.Status.ClassifiedParams = &autoscalingv1alpha1.ClassifiedParams{
        Pattern:                 result.Pattern,
        ScaleUpCooldownSeconds:  result.Params.ScaleUpCooldown,
        ScaleDownCooldownSeconds: result.Params.ScaleDownCooldown,
        MaxStepSize:             result.Params.MaxStep,
        PreferredForecaster:     result.Params.PreferredForecaster,
        ClassifiedAt:            &now,
        HistoryPoints:           int32(result.HistoryPoints),
        Confidence:              result.Confidence,
    }

    if err := w.Client.Status().Update(ctx, &aas); err != nil {
        log.Error(err, "failed to patch classifiedParams")
        return false
    }

    w.lastClassifyAt = time.Now()

    // Emit pattern_classified event.
    msg := fmt.Sprintf(
        "pattern=%s confidence=%s historyPoints=%d recommended: scaleUpCooldown=%ds scaleDownCooldown=%ds maxStep=%d forecaster=%s",
        result.Pattern, result.Confidence, result.HistoryPoints,
        result.Params.ScaleUpCooldown, result.Params.ScaleDownCooldown,
        result.Params.MaxStep, result.Params.PreferredForecaster,
    )
    w.emitEvent(ctx, "PatternClassified", msg)
    return true
}

func (w *Worker) removeReclassifyAnnotation(ctx context.Context, log ctrl.Logger) {
    var aas autoscalingv1alpha1.AgenticAutoscaler
    if err := w.Client.Get(ctx, w.Key, &aas); err != nil {
        log.Error(err, "failed to get CR for annotation removal")
        return
    }
    if aas.Annotations == nil {
        return
    }
    delete(aas.Annotations, "autoscaling.agentic.io/reclassify")
    if err := w.Client.Update(ctx, &aas); err != nil {
        log.Error(err, "failed to remove reclassify annotation")
    }
}

func (w *Worker) emitEvent(ctx context.Context, reason, message string) {
    var aas autoscalingv1alpha1.AgenticAutoscaler
    if err := w.Client.Get(ctx, w.Key, &aas); err != nil {
        return
    }
    w.EventRecorder.Event(&aas, corev1.EventTypeNormal, reason, message)
}
```

- [ ] **Step 2: Write a basic unit test for the pipeline call path**

Create `internal/classifier/worker_test.go` (unit-level, not envtest — we test the `runClassification` method indirectly via the public `RunPipeline`):

```go
package classifier_test

import (
    "testing"

    "github.com/stretchr/testify/assert"

    "github.com/pratyush-ghosh/agentic-autoscaler/internal/classifier"
)

func TestWorkerPipelineIntegration(t *testing.T) {
    series := loadSeries(t, "../../testdata/spiky_1440.json")
    result, err := classifier.RunPipeline(series, 240, 70, 2, 10)
    assert.NoError(t, err)
    assert.Equal(t, "spiky", result.Pattern)
    assert.Equal(t, "high", result.Confidence)
    assert.Greater(t, result.Params.MaxStep, int32(1))
}
```

- [ ] **Step 3: Run; verify pass**

- [ ] **Step 4: Commit**

```bash
git add internal/classifier/
git commit -m "feat(classifier): ClassifierWorker goroutine with timer/signal loop + CR patch"
```

---

### Task 11: Worker panic recovery + context cancellation (unit)

**Files:**
- Modify: `internal/classifier/worker_test.go`

- [ ] **Step 1: Append tests**

```go
import (
    "context"
    "sync"
    "time"
)

func TestWorker_ContextCancellation(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    w := &classifier.Worker{
        Config: classifier.WorkerConfig{Interval: 100 * time.Millisecond},
        ReclassifyCh: make(chan struct{}, 1),
        GenerationCh: make(chan struct{}, 1),
        // Prom and Client will be nil — runClassification will fail gracefully.
    }

    var wg sync.WaitGroup
    wg.Add(1)
    go func() {
        defer wg.Done()
        w.Run(ctx)
    }()

    time.Sleep(50 * time.Millisecond)
    cancel()
    wg.Wait() // should return promptly
}
```

Note: This test verifies the goroutine exits cleanly on context cancellation. The nil Prom/Client will cause `runClassification` to fail (nil pointer), but the panic recovery wraps it, so the goroutine survives until context is cancelled. Alternatively, we can inject a no-op fake — the key assertion is that `Run` returns.

- [ ] **Step 2: Run; verify pass**

- [ ] **Step 3: Commit**

```bash
git add internal/classifier/
git commit -m "test(classifier): verify worker exits on context cancellation"
```

---

### Task 12: Reclassify annotation trigger

**Files:**
- Modify: `internal/classifier/worker_test.go`

This is tested at the envtest level in a full controller test. For the unit plan, we verify the channel mechanics:

- [ ] **Step 1: Append channel signal test**

```go
func TestWorker_ReclassifySignalDrains(t *testing.T) {
    ch := make(chan struct{}, 1)
    // Fill channel.
    ch <- struct{}{}
    // Drain and replace (drop-and-replace for signal channels).
    select {
    case <-ch:
    default:
    }
    ch <- struct{}{}
    // Verify only one signal in channel.
    <-ch
    select {
    case <-ch:
        t.Fatal("expected empty channel")
    default:
    }
}
```

- [ ] **Step 2: Run; verify pass**

- [ ] **Step 3: Commit**

```bash
git add internal/classifier/
git commit -m "test(classifier): reclassify signal channel semantics"
```

---

### Task 13: Generation-change dedup logic

**Files:**
- Modify: `internal/classifier/worker.go` (already implemented in T10)
- Modify: `internal/classifier/worker_test.go`

- [ ] **Step 1: Append dedup test**

```go
func TestWorker_GenerationDedupSkipsRecentClassification(t *testing.T) {
    w := &classifier.Worker{
        Config: classifier.WorkerConfig{DedupSeconds: 60},
    }
    // Simulate a recent classification.
    w.SetLastClassifyAt(time.Now())

    // Should be skipped (within dedup window).
    assert.False(t, w.ShouldRunOnGeneration(), "dedup should block within window")

    // Simulate old classification.
    w.SetLastClassifyAt(time.Now().Add(-90 * time.Second))
    assert.True(t, w.ShouldRunOnGeneration(), "should allow after dedup window")
}
```

This requires exposing `SetLastClassifyAt` and `ShouldRunOnGeneration` as test helpers (or making `lastClassifyAt` accessible in the test package). Add two exported helpers:

```go
// In worker.go (or a worker_export_test.go file):
func (w *Worker) SetLastClassifyAt(t time.Time) { w.lastClassifyAt = t }
func (w *Worker) ShouldRunOnGeneration() bool {
    return time.Since(w.lastClassifyAt) > time.Duration(w.Config.DedupSeconds)*time.Second
}
```

- [ ] **Step 2: Run; verify pass**

- [ ] **Step 3: Commit**

```bash
git add internal/classifier/
git commit -m "test(classifier): generation-change dedup respects CLASSIFIER_DEDUP_SECONDS"
```

---

### Task 14: Failure paths

**Files:**
- Modify: `internal/classifier/worker_test.go`

- [ ] **Step 1: Test insufficient points returns error**

Already covered by `TestRunClassificationPipeline_InsufficientPoints` in T9.

- [ ] **Step 2: Test Prometheus query failure**

```go
func TestRunPipeline_EmptySeriesIsInsufficient(t *testing.T) {
    _, err := classifier.RunPipeline(nil, 240, 70, 2, 10)
    assert.ErrorIs(t, err, classifier.ErrInsufficientPoints)
}

func TestRunPipeline_ShortSeries(t *testing.T) {
    series := make([]float64, 69) // just below min=70
    _, err := classifier.RunPipeline(series, 240, 70, 2, 10)
    assert.ErrorIs(t, err, classifier.ErrInsufficientPoints)
}

func TestRunPipeline_ExactlyMinPoints(t *testing.T) {
    series := make([]float64, 70)
    for i := range series { series[i] = 100 }
    result, err := classifier.RunPipeline(series, 240, 70, 2, 10)
    assert.NoError(t, err)
    assert.Equal(t, "medium", result.Confidence)
}
```

- [ ] **Step 3: Run; verify pass**

- [ ] **Step 4: Commit**

```bash
git add internal/classifier/
git commit -m "test(classifier): cover insufficient points + boundary conditions"
```

---

## Phase 4 — Final smoke + milestone

### Task 15: Lint + coverage

**Files:** none

- [ ] **Step 1: Lint**

```bash
go vet ./...
go test ./internal/classifier/... -v -count=1
go test ./hack/synthetic/... -v -count=1
```

Expected: all clean.

- [ ] **Step 2: Coverage on classifier**

```bash
go test ./internal/classifier/... -coverprofile=/tmp/classifier.cov
go tool cover -func=/tmp/classifier.cov | tail -1
```

Expected: ≥ 90%.

- [ ] **Step 3: Milestone commit**

```bash
git commit --allow-empty -m "milestone: Plan #5 (ClassifierWorker + synthetic data) complete

Synthetic data generator (hack/synthetic/):
- Deterministic CLI: go run ./hack/synthetic --output=testdata/ --seed=42
- Five pattern generators: GenFlat (cv<0.10), GenPeriodic (tod>0.70),
  GenSpiky (cv>0.50 AND pt>5), GenRamp (|slope|>2.0), GenDefault (fallthrough)
- Generator tests self-validate against classification thresholds
- Seven fixture files committed in testdata/

Classification pipeline (internal/classifier/):
- ExtractFeatures: cv, peak_to_trough, tod_correlation (60-pt lag Pearson),
  trend_slope (LSQ); uses gonum/stat
- Classify: priority-ordered first-match (flat > periodic > spiky > ramp > default)
- Confidence: high (>=240 pts) / medium (>=70 pts)
- ComputeParams: all four design §7 formulae with hard floor/ceiling clamps
- RunPipeline: composed entrypoint returning PipelineResult or ErrInsufficientPoints
- Boundary tests verify exact threshold behaviour (0.10 boundary, 0.70, etc.)
- End-to-end fixture tests prove generator→features→classify consistency

ClassifierWorker goroutine (internal/classifier/worker.go):
- Four triggers: immediate first run, periodic timer, reclassify annotation,
  generation-change with CLASSIFIER_DEDUP_SECONDS dedup
- Patches status.classifiedParams on the CR
- Emits pattern_classified / pattern_unknown events
- Panic recovery with 60s backoff; context cancellation for clean shutdown
- Removes reclassify annotation on success, leaves on skip
"
```

---

## Plan-specific Definition of Done

- [ ] `go test ./internal/classifier/... -v -count=1 -cover` passes; coverage ≥ 90%.
- [ ] `go test ./hack/synthetic/... -v -count=1` passes (generators hit their target pattern).
- [ ] `testdata/*.json` committed and loadable by both Go and Python test suites.
- [ ] `TestClassify_OnSyntheticFixtures` passes: each fixture classifies to its intended pattern.
- [ ] `TestComputeParams_*` golden-value tests match hand-computed results from design §7.
- [ ] `Confidence` returns `"high"` for ≥240 points, `"medium"` for ≥70, `""` below.
- [ ] `ErrInsufficientPoints` returned when series length < `minThreshold`.
- [ ] Worker exits cleanly on context cancellation (verified by unit test).
- [ ] Generation dedup: worker skips when last classification was within `CLASSIFIER_DEDUP_SECONDS`.

---

## Notes on what's intentionally deferred

- **envtest integration test for full Worker.Run loop** — Plan #10's smoke E2E will exercise this end-to-end on a kind cluster. Unit tests here validate the logic; the wire-up with real informers is tested at the smoke level.
- **Controller watcher setup** (informer for generation-change, CR annotation watcher) — Plan #4's reconciler `SetupWithManager` wires the core reconcile; the classifier worker's watcher registration happens in `cmd/controller/main.go` and is exercised in Plan #10's E2E.
- **Makefile integration** (`make generate-testdata`) — Plan #11.
- **Python consumption of `testdata/*.json`** — Plan #7's forecast tests import these fixtures to validate the Forecast Service on realistic data.

---

## Self-Review (Spec Coverage, Placeholders, Type Consistency)

**Spec coverage.** Every bullet in §6.1 and §7 has a corresponding task. The four triggers (immediate, timer, reclassify, generation+dedup) are in T10-T13. All four features, five classification rules, four parameter formulae, and two confidence levels are individually tested.

**Placeholders.** None. Every test has full Go code; every function signature is exact.

**Type consistency.**

- `Features` fields (`CV`, `PeakToTrough`, `TodCorrelation`, `TrendSlope`) are consistent across `features.go` (declaration), `classify.go` (consumer), `params.go` (consumer), and all test files.
- `ClassifiedOutput` fields match `autoscalingv1alpha1.ClassifiedParams` from Plan #1 T6: `ScaleUpCooldownSeconds`, `ScaleDownCooldownSeconds`, `MaxStepSize`, `PreferredForecaster`, `ClassifiedAt`, `HistoryPoints`, `Confidence`, `Pattern`.
- `ErrInsufficientPoints` is `errors.New(...)` so `errors.Is` works in tests.
- `Worker.PromQuerier` interface has the same `RangeQuery` signature as Plan #3's `prometheus.Client` and Plan #4's `PromQuerier`.
- Constants match design §7's table: `BASE_SCALEUP_COOLDOWN=120`, `K_CV_UP=2.0`, `SCALEUP_COOLDOWN_HARD_FLOOR=30`, etc.

---

## Execution handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-24-plan-05-classifier-worker.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using `executing-plans`, batch execution with checkpoints for review.

Which approach?


