# Nightly E2E Runbook

The nightly E2E job is the project's continuous regression check on the
quantitative claim:

> Under the same load profile, the AgenticAutoscaler-managed target
> achieves p99 latency and 5xx rate that are *no worse* than the
> HPA-managed target by more than `TOLERANCE`.

It is **not** a PR gate — it runs on a schedule and treats failure as a
regression alarm.

## When it runs

- **Scheduled:** every day at 02:00 UTC
  (`.github/workflows/nightly-e2e.yml`)
- **On demand:** `gh workflow run nightly-e2e.yml`
  (or via the GitHub Actions UI: *Actions → Nightly E2E → Run workflow*)

The workflow input `tolerance` lets you re-run with a tighter or looser
multiplier without editing the YAML.

## Run locally

```sh
make e2e          # tolerance 1.10× (default)   — local sanity
make e2e-strict   # tolerance 1.05×              — release-candidate gate
```

Both invocations run `test/e2e/run.sh`, which:

1. Creates a kind cluster (`agentic-e2e-<pid>`).
2. Builds and loads images.
3. Helm-installs `cert-manager` and `kube-prometheus-stack`.
4. `make deploy`.
5. Waits for steady state and sleeps `WARMUP_SECONDS=300` so the
   classifier completes its first run.
6. Runs `k6/scenarios/ramp.js` for the configured profile (defaults to a
   25 m total: 5 m ramp-up + 15 m hold + 5 m ramp-down). Override per-run
   via the `ramp_up_duration`, `ramp_hold_duration`, and
   `ramp_down_duration` workflow inputs.
7. Calls `test/e2e/assertions.sh` to query Prometheus and compare both
   targets.
8. Deletes the cluster (skip with `KEEP_CLUSTER=1` to inspect).

## What the assertions check

`test/e2e/assertions.sh` issues two PromQL queries over the most recent
25-minute window and compares per-deployment values:

| Signal       | PromQL                                                                                   |
| ------------ | ---------------------------------------------------------------------------------------- |
| p99 latency  | `histogram_quantile(0.99, sum by (le) (rate(http_request_duration_seconds_bucket[25m])))` |
| 5xx rate    | `sum(rate(http_requests_total{status=~"5.."}[25m]))`                              |

Both filters narrow on `deployment="app-agentic"` vs `deployment="app-hpa"`,
which the metrics adapter populates from the pod's owning Deployment.

A signal is **only** asserted when the HPA baseline is non-zero; both
sides idle is treated as "no data" rather than a pass. This avoids
false-positive PASSes on noop runs.

## On failure

The workflow uploads `nightly-e2e-failure-artifacts` containing:

- `controller-logs.txt` — last 2000 lines from
  `agentic-system/control-plane=controller-manager`
- `forecast-logs.txt` — last 1000 lines from
  `agentic-system/app=forecast-service`
- `events.txt` — every Event in the cluster, time-sorted
- `aas.yaml` — every AgenticAutoscaler CR (full YAML)
- `deployments.txt` — `kubectl get deploy -A -o wide`

### Common root causes

- **Forecast timeout / `forecast_unavailable` Events** — Prophet warm-up
  is slow on shared CI runners; check the forecast service memory and
  the controller's `FORECAST_TIMEOUT_SECONDS`.
- **Classifier hasn't converged** — the warm-up sleep (5 min) wasn't
  enough to gather `CLASSIFIER_MIN_POINTS=70` samples. Bump
  `WARMUP_SECONDS` or shorten `CLASSIFIER_MIN_POINTS` for the test only.
- **Resource exhaustion** — kind nodes can't schedule the agentic
  target's burst above the HPA's. Check `events.txt` for
  `FailedScheduling`. The fix is to bump kind node resources, not to
  loosen the assertion.
- **Both sides idle** — k6 didn't produce load. Verify
  `TARGET_AGENTIC_URL` and `TARGET_HPA_URL` resolved to the right
  ClusterIPs in the `Run k6 ramp scenario` step.

## Artifacts retention

GitHub keeps workflow artifacts for 90 days by default; treat them as
ephemeral and copy out anything you need to reference later.
