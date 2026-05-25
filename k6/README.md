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
make k6-incluster-ramp     # runs as a Job in the demo namespace
make k6-incluster-steady
make k6-incluster-spiky
make k6-incluster-bursty
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
| `BURSTY_ITERATIONS` | bursty | `10000` |
