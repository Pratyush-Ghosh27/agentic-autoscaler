# k6 Load Scenarios

Four load-generation scenarios that drive byte-identical request streams
to both `app-agentic` (the controller's target) and `app-hpa` (the
HPA-managed control). Use them to compare tail latency, 503 rate, and
replica trajectory between the two autoscalers.

## Scenarios

| File | Pattern | Default duration | Defaults |
| --- | --- | --- | --- |
| `scenarios/ramp.js` | linear 0 → peak → hold → 0 | 25m total | peak=200 RPS |
| `scenarios/steady.js` | constant RPS | 10m | 100 RPS |
| `scenarios/spiky.js` | base load + periodic peaks | 20m | base=50, peak=500, every 2m |
| `scenarios/bursty.js` | random bursts | 15m | 50/burst, 5–30s pauses |

Every scenario respects `TARGET_AGENTIC_URL` and `TARGET_HPA_URL` (via
`lib/targets.js`); when unset, both default to `http://localhost:8080`
and `http://localhost:8081` respectively.

## Running locally

```bash
# 1. Stand up the test server (or two)
go run k6/lib/testserver.go &
PORT=8081 go run k6/lib/testserver.go &

# 2. Run a scenario
TARGET_AGENTIC_URL=http://localhost:8080 \
TARGET_HPA_URL=http://localhost:8081 \
RAMP_UP_DURATION=10s RAMP_HOLD_DURATION=10s RAMP_DOWN_DURATION=10s \
RAMP_RPS_PEAK=20 \
k6 run k6/scenarios/ramp.js
```

## Dry-run validation

`dry-run_test.go` invokes each scenario via `k6 run --vus=1 --iterations=5`
against an in-process `httptest.Server`, asserting all `check()`s pass and
k6 exits 0. Requires `k6` on PATH; the test self-skips when it's missing.

```bash
go test -tags=integration -v ./k6/...
```

## Env-var reference

| Var | Scenario | Default |
| --- | --- | --- |
| `TARGET_AGENTIC_URL` | all | `http://localhost:8080` |
| `TARGET_HPA_URL` | all | `http://localhost:8081` |
| `RAMP_UP_DURATION` | ramp | `5m` |
| `RAMP_HOLD_DURATION` | ramp | `15m` |
| `RAMP_DOWN_DURATION` | ramp | `5m` |
| `RAMP_RPS_PEAK` | ramp | `200` |
| `STEADY_RPS` | steady | `100` |
| `STEADY_DURATION` | steady | `10m` |
| `SPIKE_BASE_RPS` | spiky | `50` |
| `SPIKE_PEAK_RPS` | spiky | `500` |
| `SPIKE_INTERVAL` | spiky | `2m` |
| `SPIKE_DURATION` | spiky | `30s` |
| `SPIKY_TOTAL_DURATION` | spiky | `20m` |
| `BURST_SIZE` | bursty | `50` |
| `BURST_MIN_INTERVAL` | bursty | `5` |
| `BURST_MAX_INTERVAL` | bursty | `30` |
| `BURSTY_TOTAL_DURATION` | bursty | `15m` |
| `BURSTY_ITERATIONS` | bursty | `10000` |
