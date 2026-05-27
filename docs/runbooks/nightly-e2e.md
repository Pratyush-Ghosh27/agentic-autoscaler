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

The workflow inputs let you re-run with different parameters without editing the YAML:

- `tolerance` — multiplier for the agentic-vs-HPA delta. **Default is `1.10`** — the release-gate value. Tighten to `1.05` for release candidates; loosen to `1.20`+ only for runs on a hot CI runner where variance dominates.
- `ramp_up_duration` / `ramp_hold_duration` / `ramp_down_duration` — k6 ramp scenario phases (defaults `5m` / `15m` / `5m`).

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
6. Runs `k6/scenarios/ramp.js` as an in-cluster Job (defaults to a
   25 m total: 5 m ramp-up + 15 m hold + 5 m ramp-down). Override per-run
   via the `ramp_up_duration`, `ramp_hold_duration`, and
   `ramp_down_duration` workflow inputs.
7. Calls `test/e2e/assertions.sh` to query Prometheus and compare both
   targets on **p99 latency** and **5xx rate**.
8. **(v2 / Plan 18)** Patches the agentic CR with `spec.preferredForecaster: gbdt_quantile`, sleeps 90 s for one reconcile cycle, then runs `k6/scenarios/spiky.js` (20 m fixed) to drive realistic spiky load through the gbdt path.
9. **(v2 / Plan 18)** Calls `test/e2e/assertions-gbdt.sh` to assert `forecast_dispatch_total{model_used="gbdt_quantile"} > 0`, locking in the gbdt path on every nightly. The classifier-promotion path is **not** exercised here (it requires `CLASSIFIER_MIN_POINTS=72` of 5-min-cadence history ≈ 6 h, which a single nightly cannot satisfy); patching `preferredForecaster` directly tests the dispatch path under realistic spiky load.
10. Deletes the cluster (skip with `KEEP_CLUSTER=1` to inspect).

## What the assertions check

The nightly runs **two** assertion scripts:

### 1. `test/e2e/assertions.sh` (after the ramp scenario)

Two PromQL queries over the most recent 25-minute window, compared per-deployment:

| Signal       | PromQL                                                                                   |
| ------------ | ---------------------------------------------------------------------------------------- |
| p99 latency  | `histogram_quantile(0.99, sum by (le) (rate(http_request_duration_seconds_bucket[25m])))` |
| 5xx rate    | `sum(rate(http_requests_total{status=~"5.."}[25m]))`                              |

Both filters narrow on `deployment="app-agentic"` vs `deployment="app-hpa"`,
which the metrics adapter populates from the pod's owning Deployment.

A signal is **only** asserted when the HPA baseline is non-zero; both
sides idle is treated as "no data" rather than a pass. This avoids
false-positive PASSes on noop runs.

### 2. `test/e2e/assertions-gbdt.sh` (after the spiky scenario — v2 / Plan 18)

| Signal | PromQL | Pass criterion |
|---|---|---|
| gbdt path exercised | `forecast_dispatch_total{model_used="gbdt_quantile"}` | `> 0` |

Diagnostic context is also reported (`linear_extrap` and `prophet` counts) so a failed assertion's log immediately indicates which failure mode hit:

| `gbdt_quantile` | other counts | Likely cause |
|---|---|---|
| `0` | `> 0` | CR patch didn't take, controller isn't forwarding `preferredForecaster`, or every gbdt call fell back (check forecast logs for `gbdt_quantile failed, falling back to linear_extrap`) |
| `0` | all `0` | Forecast service isn't being scraped (check ServiceMonitor + Prometheus target health) |
| NaN | NaN | `forecast_dispatch_total` series doesn't exist — forecast service down or metric unregistered |

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
  enough to gather `CLASSIFIER_MIN_POINTS=72` samples (v2 default; was
  70 in v1). At the v2 cold-path resolution of 5 min, 72 points ≈ 6 h
  of history, so the nightly's warm-up alone won't satisfy this gate —
  the spiky-scenario gbdt assertion (Plan 18) deliberately bypasses the
  classifier by patching `preferredForecaster` directly. Bump
  `WARMUP_SECONDS` only if you need classifier-driven scaling decisions
  during the ramp scenario.
- **`gbdt_quantile` dispatch count is 0 (Plan 18 assertion)** — see the
  triage table in *What the assertions check → 2.* above. Check
  `forecast-logs.txt` for `gbdt_quantile failed` lines, and verify the
  `kubectl patch` step in the workflow log shows
  `spec.preferredForecaster=gbdt_quantile` was applied before the spiky
  k6 Job started.
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
