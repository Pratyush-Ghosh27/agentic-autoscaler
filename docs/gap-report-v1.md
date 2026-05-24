# Design vs Shipped — Gap Report (v1.0.0)

**Date:** 2026-05-24
**Tag:** `v1.0.0` at `43725b3`
**Reviewer:** read-through of `docs/design.md` (800 lines) against the live
  implementation in `cmd/`, `internal/`, `api/`, `target-app/`, `forecast-service/`,
  `deploy/`, `test/`.

The PR CI is green and the nightly E2E ran end-to-end with `success` once. This
report documents what *actually* works versus what the design promises, so v2
planning starts from reality, not from "all green ⇒ all done".

> **Status as of 2026-05-24** (branch `fix/gaps-v1.1`):
> - ✅ G1, G2, G3, G4, G5, G7 — fixed.
> - ✅ G8 (NEW) — target-app metric names didn't match the controller's
>   PromQL (`target_app_*` vs `http_*`); fixed in same PR.
> - ✅ G9 (NEW) — kube-prometheus-stack only consumes PodMonitor/ServiceMonitor
>   CRDs, ignoring the `prometheus.io/scrape` annotations the target-app
>   was relying on. PodMonitor added in same PR.
> - ⏳ G6 — left for after a few nightly runs with real data.

> **Bottom line.** The hot path (forecast-driven reconcile + webhook + status)
> is real and exercised. The cold path (pattern classification) is dead code
> in the deployed binary. The quantitative comparison vs HPA in the nightly
> E2E is silently a no-op — the assertion currently passes by *skipping* both
> checks. There are no functional regressions on `main`, but the v1 promise of
> "agentic ≤ HPA on p99 + 5xx, with classifier-tuned cooldowns" is not yet
> demonstrated by CI.

---

## 1. What shipped and works

| Capability | Evidence |
|---|---|
| `AgenticAutoscaler` CRD with all §4 fields | `api/v1alpha1/agenticautoscaler_types.go` 1:1 with design |
| Validating webhook with all §4 rules | `internal/webhook/v1alpha1/validator.go` + 17 webhook tests |
| Hot-path reconciler — Prometheus query → Forecast → `/scale` patch | `internal/controller/agenticautoscaler_controller.go` |
| Per-CR `rps_per_pod` ring buffer + steady-state gate + clamp | reconciler step 5; restart-recovery from `status.rpsPerPodCurrent` |
| `maxStepSize` cap and scale-up/down cooldowns | reconciler steps 6–7 |
| Hysteresis (no patch when `target == current`) | reconciler step 8 |
| All 14 reasoning tokens emitted as Event reasons | `internal/reasoning/tokens.go` matches design §5 + §6 |
| Kill-switch annotation pre-check + `Disabled` phase | reconciler step 1 |
| HPA conflict pre-check + `Conflict` phase | reconciler step 1 |
| `forecast_unavailable` / `metrics_unavailable` no-op-and-retry behaviour | confirmed in last smoke run (`metrics_unavailable: 0 range samples`) |
| Forecast Service — Prophet + linear_extrap + auto-select + warm-up | `forecast-service/src/forecast/dispatch.py` |
| ExplainWorker drop-and-replace channel + Ollama integration | `internal/explainer/worker.go` + wired in `cmd/controller/main.go` |
| Target app with semaphore-bounded `/work` returning 503 on overload | `target-app/internal/server/server.go` |
| k6 ramp / steady / spiky / bursty scenarios | `k6/scenarios/*.js` |
| PR CI: lint + go test + python test + smoke | green on `cf5dc7a` (PR #1) |
| Nightly E2E: deploy + ramp + assertions | run #2 conclusion `success` |

---

## 2. Gaps in priority order

### G1 (CRITICAL) — `ClassifierWorker` is dead code

**Evidence.** `cmd/controller/main.go` does not import `internal/classifier`,
does not instantiate a `classifier.Worker`, and does not start its goroutine.
Grep confirms zero call sites outside the unit/integration test suite.

**Effect.**

- `status.classifiedParams` is never written → the reconciler always falls
  through to env-var defaults (`DEFAULT_SCALE_UP_COOLDOWN_SECONDS=60s`,
  `DEFAULT_SCALE_DOWN_COOLDOWN_SECONDS=300s`, `DEFAULT_MAX_STEP_SIZE=4`).
- `pattern_classified` and `pattern_unknown` events are never emitted.
- The `kubectl get aas` `Pattern` printcolumn is always empty.
- Three Grafana panels keyed off
  `agenticautoscaler_classified_pattern` are dead (G2 below).
- The design's central differentiator vs a basic HPA — *the autoscaler tunes
  itself to the workload's traffic shape* — is not active in production.

**The good news.** The classifier *implementation* is fully there:
`internal/classifier/{worker.go, pipeline.go}` is 100% covered by tests, the
formulae and feature extraction match design §7 exactly, and the goroutine
machinery (timer + reclassify + generation signals + dedup) is implemented.
It just needs to be instantiated and started in `main.go` — probably 30–50
lines plus a Deployment-watcher informer set up.

**Severity.** High. This is the most user-visible gap.

**Estimated fix.** Half a day of careful wiring + integration test that the
classifier writes `classifiedParams` within `CLASSIFIER_INTERVAL_MINUTES` of
sufficient history.

---

### G2 (CRITICAL) — Controller emits no custom Prometheus metrics

**Evidence.** No `prometheus.NewCounter`, `promauto`, or `MustRegister` calls
anywhere in `internal/`. The only metrics endpoint enabled is the
controller-runtime defaults (reconcile rate, queue depth, errors).

**Effect.** The Grafana dashboard (`deploy/grafana/agentic-dashboard.json`)
queries three series that don't exist:

| Panel | PromQL | Status |
|---|---|---|
| Predicted RPS | `agenticautoscaler_predicted_rps{namespace="demo"}` | dead |
| Scale events by reason | `sum(increase(agenticautoscaler_events_total[5m])) by (reason)` | dead |
| Classified pattern | `agenticautoscaler_classified_pattern{namespace="demo"}` | dead |

Three of seven dashboard panels are blank. The remaining four (actual RPS,
deployment replicas, p99 latency, 5xx rate) get their data from the target-app
and kube-state-metrics — but see G3 for why even those are partially broken.

**Severity.** High. Without these metrics, operators have no way to see *why*
the controller decided what it decided, except by tailing logs or scrolling
through `kubectl describe aas` events.

**Estimated fix.** Half a day. Register a `GaugeVec` for predicted RPS keyed
by `(namespace, name)`, a `CounterVec` for scale events keyed by
`(namespace, name, reason)`, and an enum-style `GaugeVec` for the classified
pattern. Wire from the reconciler step 11 (status update) and step 10 (event
emit) and from the classifier's `runClassification`.

---

### G3 (CRITICAL) — `target_app_*` metrics lack a workload-identifying label

**Evidence.** `target-app/internal/server/server.go` registers the histogram
with labels `[]string{"path"}` and the counter with `[]string{"path", "status"}`.
Neither metric carries a `deployment`, `app`, or `workload` label.

**Effect.** Every PromQL query in `assertions.sh` and the dashboard that
filters by `{deployment="app-agentic"}` matches **nothing**:

```bash
# assertions.sh line 42
histogram_quantile(0.99, sum by (le) (
  rate(target_app_request_duration_seconds_bucket{deployment="app-agentic"}[25m])
))
# → empty result → NaN
```

The assertion script *guards* against this with:

```python
if not math.isnan(p99_h) and p99_h > 0:
    if math.isnan(p99_a) or p99_a > p99_h * t:
        failures.append(...)
else:
    print("warning: p99 hpa baseline is 0 or NaN; skipping latency assertion")
```

So the assertion silently skips both p99 and 5xx checks. **The "passed" nightly
run on `cf5dc7a` did not verify the design's central SLO claim.** It only
verified that the load-driving and port-forward plumbing works.

**Severity.** Critical. This makes the quantitative E2E a false-positive
generator — it will pass even if the agentic side is *worse* than HPA.

**Estimated fix.** A few hours. Two options:

- **Option A** (cleaner): Add a `deployment` label to both metrics, populated
  from `os.Getenv("DEPLOYMENT_NAME")`, and set that env var via the downward
  API in both `target-agentic.yaml` and `target-hpa.yaml`.
- **Option B**: Add a Prometheus relabel rule via a `PodMonitor` /
  `ServiceMonitor` that derives `deployment` from `pod` via the
  `kube_pod_owner` join — but this is more fragile and requires kube-state-metrics
  (currently disabled in the chart values).

Option A is preferred. After fixing, also tighten `assertions.sh` to *fail*
(not just warn) when the baseline is NaN — turning silent-skip into
loud-failure prevents this regression class entirely.

---

### G4 (HIGH) — Prometheus Adapter not installed → HPA can't actually scale

**Evidence.** `deploy/manifests/hpa.yaml` targets the
`http_requests_per_second` Pod metric, which requires the
[prometheus-adapter](https://github.com/kubernetes-sigs/prometheus-adapter)
Helm chart. The hpa.yaml comment itself says:

> *"Without the adapter the HPA will report `FailedGetResourceMetric` and
> stay at `minReplicas`, which is acceptable for a controller-comparison demo."*

The adapter isn't in `deploy/helm/` and isn't installed by `make install-deps`.

**Effect.** The HPA stays at `minReplicas=2` for the entire run, regardless of
load. Combined with G3, the agentic-vs-HPA comparison is structurally rigged:

- HPA can't scale (G4) → its p99 will tail off badly under load → trivial
  agentic win.
- Even if HPA could scale, its metrics aren't observable (G3) → assertion
  silently skips.

So the apples-to-apples comparison the design promises (§3 architecture
diagram, two deployments under identical traffic) is currently
apples-to-cardboard.

**Severity.** High. Either fix it (install the adapter, populate the metric)
or rewrite the comparison to use a CPU-based HPA (no adapter needed but
arguably less honest).

**Estimated fix.** Half a day. Add `prometheus-adapter` to
`deploy/helm/install-deps.yaml` with values exposing
`http_requests_per_second` from the same Prometheus that scrapes the
target-app. After fixing G3, the metric query becomes well-defined. After
fixing G4, the HPA actually moves replicas. Together they unlock the actual
comparison.

---

### G5 (MEDIUM) — Ollama in nightly E2E doesn't exercise the explainer

**Evidence.** `nightly-e2e.yml` installs Ollama and pre-pulls `phi3` on the
runner host, but `config/manager/manager.yaml` leaves `OLLAMA_URL` unset
(defaulting to `http://localhost:11434` from inside the controller pod, which
is the pod itself, not the runner host) and sets `OLLAMA_MODEL=llama3.2`
(mismatched against the workflow's `phi3`).

**Effect.** The ExplainWorker logs "connection refused" on every scale event.
No `scale_explained` events are emitted. The Ollama install + model pull
contributes ~3 minutes of CI time for zero functional benefit.

**Severity.** Medium. Doesn't affect SLO assertions, but the design's promise
("every replica-changing event followed by a `scale_explained` Event") is
not exercised end-to-end.

**Estimated fix.** Two paths:

- **Lazy fix**: drop the Ollama install + model pull from `nightly-e2e.yml`.
  ~5 line removal. Saves CI time. Honest about the worker not being
  exercised in CI (it's already covered by unit + integration tests).
- **Proper fix**: deploy Ollama as an in-cluster `Pod` (or sidecar to the
  controller), set `OLLAMA_URL` to the in-cluster service, and align the
  model name. Adds an e2e check that exactly one `ScaleExplained` event is
  emitted per replica change.

Lazy first, proper later if explainer-in-CI becomes a priority.

---

### G6 (MEDIUM) — `make e2e` and `nightly-e2e.yml` use `tolerance=1.25`, not the design's release-candidate gate

**Evidence.** `Makefile` has both `make e2e` (TOLERANCE=1.10) and
`make e2e-strict` (TOLERANCE=1.05). `nightly-e2e.yml` defaults to 1.25 with
the comment "shared CI runners where variance dominates".

**Effect.** None today, because G3 makes the assertion skip. But once G3 is
fixed, 1.25× is a generous gate — agentic could be 25% slower on p99 and
still pass. For a *regression alarm* that's appropriate. For a *release gate*
on `main`, 1.10× is more useful.

**Severity.** Medium. Tighten only after G3 is fixed and we've seen 5+ runs
with real assertion data to gauge variance.

---

### G7 (LOW) — k6 ramp scenario duration input now wired, but only the hold phase

**Evidence.** Today's fix wired `inputs.ramp_duration` →
`RAMP_HOLD_DURATION`. Up + down phases remain hardcoded at 5 min each.

**Effect.** Operators can't independently shorten the up/down phases. For
"give me a quick 10-min sanity check" they have to edit `ramp.js` directly.

**Severity.** Low. Only matters if you frequently run shortened nightlies for
spot-checks. Out-of-CI (`make k6-ramp`) is fine because env vars work
locally.

**Estimated fix.** ~10 minutes. Add `ramp_up_duration` and `ramp_down_duration`
inputs and plumb to `RAMP_UP_DURATION` / `RAMP_DOWN_DURATION`.

---

## 3. What's deferred from the design but doesn't block v1

Items the design lists in scope but were never implemented; calling them out
so they don't get rediscovered as "regressions":

- **`forecast_prophet_failures_total` counter** (design §9 row 1). The
  fallback works; the counter doesn't exist. Roll into G2.
- **`status.lastScaleTime` cooldown seeding on restart** (design §5 step 5).
  The reconciler reads it; needs an envtest that proves a cold restart
  honours the cooldown rather than re-firing.
- **`autoscaling.agentic.io/reclassify` annotation removal** (design §6.1
  trigger 3). Implemented in `classifier/worker.go` but never reachable today
  because the worker isn't started (G1). Verify after G1 is fixed.

---

## 4. Recommended v2 sequence

If v2's first release is "make the design true", the order is forced by
dependencies:

1. **G3 first** — without `deployment` labels, nothing else can be measured.
   Half a day; immediately turns the nightly into a real regression alarm.
2. **G4** — install prometheus-adapter so the HPA actually scales. Half a
   day. Now agentic-vs-HPA is a real comparison.
3. **G1** — wire ClassifierWorker into `main.go`. Half a day. Now the
   autoscaler is *actually* agentic.
4. **G2** — register custom metrics. Half a day. Grafana dashboard becomes
   useful for the first time.
5. **G5 (lazy)** — drop Ollama from nightly. 5 minutes. Save 3 min/run.
6. After 5 nightly runs with real data: **G6** — tighten tolerance to 1.10×.

Total: ~2.5–3 days of careful work to close the v1 design gap. Plan one PR
per gap, all gated by the same nightly E2E (which gets steadily stricter as
each gap closes).

If v2's first release is *new* features beyond the design, lift G3 + G4 +
G1 first regardless — without them there's no honest baseline to measure
new features against.
