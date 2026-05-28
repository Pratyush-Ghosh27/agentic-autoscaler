# Synthetic Fixture Schema

> **Scope:** these JSON files are **unit-test fixtures only**, consumed
> directly by the Go classifier suite and the Python forecast-service
> suite. They are *not* designed to be pushed into a live cluster's
> Prometheus — `hack/synthetic/main.go` anchors every fixture's
> `timestamp_unix` to a fixed Unix epoch (`1716500000` =
> `2024-05-23T20:53:20 UTC`) so regenerations stay deterministic and
> git diffs stay tractable. Backfilling a recent-history Prometheus
> from these files would require re-stamping every point relative to
> `now` and enabling Prometheus' `--web.enable-remote-write-receiver`
> (kube-prometheus-stack disables it by default).

Each `.json` file is an array of `DataPoint` objects:

```json
[
  {"timestamp_unix": 1716504000, "rps": 120.5},
  {"timestamp_unix": 1716504060, "rps": 118.2}
]
```

Fields:

- `timestamp_unix`: integer epoch seconds, 60s apart (1-min resolution).
- `rps`: float64, non-negative.

Filename convention: `<pattern>_<point_count>.json`.

## Regenerate

```sh
go run ./hack/synthetic --output=testdata --seed=42
```

Deterministic — same seed produces the same output. Commit the outputs so
both the Go classifier tests and the Python forecast service tests can
load them as golden inputs.

## Patterns

| File | Generator | Target features |
| --- | --- | --- |
| `flat_1440.json` | `GenFlat` | `cv < 0.10` |
| `periodic_1440.json` | `GenPeriodic` | `hourly_autocorr > 0.70` (named `tod_correlation` in v1) |
| `spiky_1440.json` | `GenSpiky` | `cv > 0.50` AND `peak_to_trough > 5` |
| `gradual_ramp_1440.json` | `GenRamp` | `\|trend_slope\| > 2.0 rps/min` |
| `default_1440.json` | `GenDefault` | none of the above triggers |
| `flat_70.json` | `GenFlat` | v1-era minimum-confidence boundary (70 points). At v2's `CLASSIFIER_MIN_POINTS=72` default this fixture is *below* the boundary; the classifier will emit `pattern_unknown` on it. Useful precisely for testing that branch. |
| `insufficient_50.json` | `GenFlat` | below `CLASSIFIER_MIN_POINTS` (any version) |
