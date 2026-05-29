# k6 Load Scenarios

Seven load-generation scenarios that drive byte-identical request streams
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
| `scenarios/diurnal.js` | 24-stage day-shape with lunch/PM spikes | 24h (configurable) | base=20, peak=300, spike=500 |
| `scenarios/rotating.js` | continuous 4-pattern cycle for forecast accuracy | 23h20m (10 cycles) | steady=100, ramp peak=200, spike=200, bursty [60,140] |
| `scenarios/stress.js` | 7m baseline + 3m oversized spike, repeated | 60m (6 cycles) | baseline=200, spike=600 — tuned to expose HPA 503 gap |

Every scenario respects `TARGET_AGENTIC_URL` and `TARGET_HPA_URL` (via
`lib/targets.js`); when unset, both default to `http://localhost:8080`
and `http://localhost:8081` respectively.

## Running against a kind/k8s cluster — read this first

> **Don't use `kubectl port-forward svc/...` for autoscaler comparisons.**
>
> `kubectl port-forward svc/X` does *not* load-balance — it picks one
> Endpoint at session start and pins all traffic to that one pod for
> the session's lifetime ([kubernetes/kubernetes#15180][k8s-15180]).
> If `app-agentic` scales from 2 to 10 replicas, only one of those 10
> pods receives any traffic; the other 9 sit idle. The HPA's per-pod
> RPS metric averages across all replicas (1 hot, N-1 cold), so it
> stays near zero and the HPA never scales. Both reconcilers bottleneck
> on a single pod, making their tail latencies and 5xx rates
> artificially equal — the comparison silently degrades from
> "agentic vs HPA" to "single-pod-A vs single-pod-B".
>
> The correct way to drive load against a multi-replica autoscaled
> workload from outside the cluster is to run the load generator
> *inside* the cluster, where it can hit the Service ClusterIP and
> get real kube-proxy load-balancing.
>
> [k8s-15180]: https://github.com/kubernetes/kubernetes/issues/15180

### In-cluster Job (canonical for autoscaler comparison)

```bash
make k6-incluster-ramp       # runs as a Job in the demo namespace
make k6-incluster-steady
make k6-incluster-spiky
make k6-incluster-bursty
make k6-incluster-diurnal    # 24h diurnal cycle (set DIURNAL_TOTAL_HOURS=N to compress)
make k6-incluster-rotating   # 24h continuous 4-pattern rotation (forecast accuracy demo)
make k6-incluster-stress     # 60m oversized-spike loop (503-rate-gap demo)
```

Each target wraps `deploy/k6/run-incluster.sh <scenario>`, which:

1. Rebuilds the `k6-scripts` ConfigMap from `k6/{lib,scenarios}/`.
2. Recreates `Job/k6-<scenario>` in `demo` (Pods get the cluster's
   default DNS so `app-agentic.demo.svc.cluster.local` resolves).
3. Streams the Pod's stdout to your terminal until the Job finishes.

Override scenario tunables via env vars before invoking, e.g.

```bash
RAMP_UP_DURATION=2m RAMP_HOLD_DURATION=10m RAMP_RPS_PEAK=300 make k6-incluster-ramp
```

### Continuous 24h traffic (round-robin loop)

For multi-hour soak runs that exercise *several* traffic shapes back-to-back —
useful for showing the controller's classifier flip between
`PatternGradualRamp` / `PatternFlat` / `PatternPeriodic` / `PatternSpiky`
as patterns change — use the loop wrapper:

```bash
make k6-incluster-24h-loop                                      # 24h, all 4 short scenarios
DURATION_HOURS=6 make k6-incluster-24h-loop                     # 6h smoke cycle
SCENARIOS="ramp steady" make k6-incluster-24h-loop              # subset only
COOLDOWN_SECONDS=120 make k6-incluster-24h-loop                 # longer pause between scenarios
```

The loop wrapper (`deploy/k6/run-24h-loop.sh`) sequences `ramp → steady → spiky → bursty`
in round-robin, deletes each Job after it finishes, sleeps `COOLDOWN_SECONDS`,
then starts the next one. It honours every per-scenario env var the wrapper
script does. Diurnal is intentionally excluded from the rotation — for a
*continuous* day-shape time series (single uninterrupted history that lets
Prophet's hour-baseline regressor engage), use `make k6-incluster-diurnal`
instead.

| Var | Default | Notes |
| --- | --- | --- |
| `DURATION_HOURS` | `24` | float OK (e.g. `0.5` for 30-min smoke) |
| `SCENARIOS` | `"ramp steady spiky bursty"` | space-separated subset |
| `COOLDOWN_SECONDS` | `60` | gap between scenarios so the classifier window doesn't blur boundaries |

### Host-mode k6 (single-pod; debugging only)

The `make k6-{ramp,steady,spiky,bursty}` targets exist but run k6 on
your host hitting `localhost:8080`/`8081`. That requires manual
`kubectl port-forward svc/...` and inherits the single-pod-pinning
problem above. **Don't use these to evaluate scaling.** They're useful
when you want to send a known traffic shape to a known single pod
(e.g. reproducing a bug). Manual setup:

```bash
# 1. Stand up the test server (or two), or port-forward into a
#    real cluster (single-pod-pinned, see warning above):
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
| `BURSTY_ITERATIONS` | bursty | `10000` [^1] |
| `DIURNAL_BASE_RPS` | diurnal | `20` |
| `DIURNAL_PEAK_RPS` | diurnal | `300` |
| `DIURNAL_SPIKE_RPS` | diurnal | `500` |
| `DIURNAL_TOTAL_HOURS` | diurnal | `24` (compresses/expands the 24-stage shape) |
| `ROTATING_CYCLES` | rotating | `10` (10 × 140m = 23h20m) |
| `ROTATING_STEADY_RPS` | rotating | `100` |
| `ROTATING_RAMP_PEAK_RPS` | rotating | `200` |
| `ROTATING_SPIKE_RPS` | rotating | `200` |
| `ROTATING_BURSTY_FLOOR` | rotating | `60` |
| `ROTATING_BURSTY_CEILING` | rotating | `140` |
| `STRESS_CYCLES` | stress | `6` (6 × 10m = 60m) |
| `STRESS_BASELINE_RPS` | stress | `200` (flat between spikes) |
| `STRESS_SPIKE_RPS` | stress | `600` (oversized to force HPA failure) |
| `STRESS_BASELINE_MIN` | stress | `7` (minutes at baseline per cycle) |
| `STRESS_SPIKE_MIN` | stress | `3` (minutes at spike per cycle) |

[^1]: The bursty scenario uses the `per-vu-iterations` executor whose
    progress bar is scaled to `iterations`, not elapsed time. At the
    default `BURSTY_ITERATIONS=10000` with a single VU producing ~3-4
    iterations per minute, the bar reads ~0% for the entire run;
    `BURSTY_TOTAL_DURATION` is the real terminator. Set
    `BURSTY_ITERATIONS=60` (roughly what a 15m run actually completes)
    for an honest progress bar. The default is intentionally high so
    overriding `BURSTY_TOTAL_DURATION` upward for soak runs doesn't
    accidentally short-circuit on iterations.
