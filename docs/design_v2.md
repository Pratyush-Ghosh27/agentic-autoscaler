# **Agentic Autoscaler — Design Spec**

Date: 2026-05-25 Status: Draft for team review (v2 — complete spec; supersedes v1 dated 2026-05-21)

> Revision notes (2026-05-25):
> * Pass 1 (post-audit, findings 1, 2a, 3a, 4a, 5, 6, 8, 10): the most consequential change vs. the earlier v2 draft is that `forecast_hw_seasonal` was removed — the periodic-pattern forecaster is now `prophet` with the hourly regressor, which already captures the seasonal level cleanly without the design ambiguities of the HW seeding scheme.
> * Pass 2 (post-review, findings 11–16): raised `CLASSIFIER_MIN_POINTS` from 24 → 72 (~6h at 5-min) and `CLASSIFIER_HIGH_CONFIDENCE_POINTS` from 48 → 240 (~20h) so classifier features (cv, p95, hourly_autocorr) are statistically meaningful and "high" confidence essentially guarantees `hourlyProfileValid`. Wired `context.trend_24h_slope` into `forecast_linear_extrap` as a noise-reduction prior (new env `LINEAR_EXTRAP_TREND_BLEND`). Renamed the cooldown constant `K_TOD_DOWN → K_PERIODIC_DOWN` in the spec (source rename tracked separately). Fixed the `historyPoints: 1440` example, the `Long-term context` prose-line gating, and the architecture diagram's cold-path bullet.
> * Pass 3 (deeper audit, findings 17–24):
>   * **F17** (bug fix): added `current_minute_utc` to the `/recommend` context. The pass-1 `current_hour_utc`-only anchor left the ds construction's minute component undefined, which silently mislabeled ~30 of 60 Prophet/GBDT training rows by one UTC hour and effectively neutralised the hourly regressor.
>   * **F18** (bug fix): pinned `trend_24h_slope` units to **rps/min** with explicit `/ CONTEXT_DOWNSAMPLE_RESOLUTION_MIN` division. The pass-2 wiring of trend into `forecast_linear_extrap` would otherwise have blended a 5×-too-large slope value (rps/5-min-bucket) into a 1×-correct one (rps/min).
>   * **F19** (bug fix): switched the third re-classification trigger from `Deployment.metadata.generation` to the `deployment.kubernetes.io/revision` annotation. The generation field is bumped on every `/scale` patch, which would have caused this trigger to fire on every reconcile that scales — defeating its purpose. The revision annotation only changes on actual rollouts (image / env / command edits).
>   * **F20** (correctness improvement): seed the per-CR `rps_per_pod` ring buffer with **5 copies** of the persisted `status.rpsPerPodCurrent` on restart, not a single copy. The old single-copy seed was washed out by the second new observation; the new seed preserves the persisted estimate across the next 5+ observations, matching the documented intent.
>   * **F21** (spec ambiguity): pinned GBDT's timestamp-derived features (`hour_of_day_baseline`, `minute_in_hour`) to the same anchored ds array Prophet uses. Without this, the hour/minute features had undefined provenance.
>   * **F22** (clarification): explicit note that `auto` mode never returns `gbdt_quantile`. The GBDT path is intentionally opt-in — driven by the classifier's pattern decision or an explicit operator override only.
>   * **F23** (operability): made the steady-state RPS noise floor an env var (`RPS_PER_POD_NOISE_FLOOR_RPS`, default 10), so operators with intentionally-low-RPS workloads can tune it.
>   * **F24** (consistency): added `GBDT_MIN_POINTS` env var, mirroring `PROPHET_MIN_POINTS`. Both default to 30.
> * Pass 4 (focused review of §7 + recommendation semantics, findings 25–30):
>   * **F25** (cross-reference): made §7 explicit that `trend_slope` (classifier feature) **is** `context.trend24hSlope`, computed once in §6.1 step 6.5 in rps/min. Eliminates the risk of an implementer recomputing it without F18's unit conversion.
>   * **F26** (calibration): replaced the `gradual_ramp` rule's absolute threshold (`abs(trend_slope) > 2.0 rps/min`, which fired only on 30×+ daily growth) with a relative threshold (`abs(trend_slope) * 1440 / max(mean, 1) > GRADUAL_RAMP_DAILY_DRIFT_FRAC`, default 0.20). Scale-invariant — 20% projected daily drift fires at any RPS level.
>   * **F27** (visibility): rewrote the `recommendedReplicas` semantics to honestly reflect that it's clamped to `[minReplicas, maxReplicas]`. Added `unboundedRecommended` to the K8s event message and new reasoning tokens `max_replicas_binding` / `min_replicas_binding` so operators can see when the CRD bound is the binding constraint. Added precedence rules between the new tokens and `step_capped_*` / `cooldown_holding_*`.
>   * **F28** (scale-awareness): replaced `peak_to_trough` denominator `mean(series) + 1` with `max(mean(series), 1.0)` — same behaviour at high RPS, removes the low-RPS bias where the additive `+1` regulariser dominated.
>   * **F29** (constants discipline): named the magic threshold `CV_GUARD_MEAN_RPS = 1.0` (used in `cv`'s zero-guard).
>   * **F30** (documentation): added a paragraph explaining why `periodic` outranks `spiky` in classification priority — Prophet's hourly regressor + 2× p95 cap is the right tool for periodic-spiky workloads where periodicity is the dominant signal.
> * Pass 5 (final review, findings 31–33):
>   * **F31** (bug fix): `forecast_linear_extrap` now recomputes `b = mean(y) - m * mean(x)` after blending `m`. The pass-2 wiring changed `m` without recomputing `b`, which rotated the line around `x=0` instead of around the data's centroid and biased predictions by `(m_blended - m_original) * (n-1)/2` RPS. For a 60-min default window with even modest slope divergence, this was on the order of tens of RPS — large enough to materially affect replica decisions.
>   * **F32c** (consistency): `CV_GUARD_MEAN_RPS` moved from §7 hardcoded constants to §4 cold-path env vars (operator-tunable; depends on the workload's RPS scale). `GRADUAL_RAMP_DAILY_DRIFT_FRAC` stays as a hardcoded constant in §7 with a sentence explaining why (it defines what `gradual_ramp` *means* in this autoscaler; tuning it would silently shift the meaning of the pattern label).
>   * **F33** (UX gap from F27b): added prompt conditionals for `max_replicas_binding` and `min_replicas_binding` reasoning tokens. Without these, the LLM saw only the bare `Scaling: 5 → 10 (max_replicas_binding)` line and generated misleading prose ("scaled up to handle load") that hid the actual capacity-planning signal. New lines surface `unboundedRecommended` and the binding CRD bound directly to the operator. Plumbed `UnboundedRecommended`, `MaxReplicas`, `MinReplicas` into ExplainRequest fields.

## **1\. Overview**

Kubernetes HPA is reactive: by the time CPU crosses the threshold, latency has already risen. The agentic autoscaler is a real Kubernetes operator that polls Prometheus for recent RPS history, asks a Forecast Service (auto-selecting between three forecasters: linear extrapolation, Prophet, and GBDT quantile) to predict RPS `FORECAST_HORIZON_MINUTES` ahead (default: 10 minutes), and patches the target Deployment's `/scale` subresource so capacity arrives before traffic does.

In Grafana, the predicted-RPS line visibly leads the actual-RPS line, and replicas scale up before the actual load arrives. Under identical traffic, `app-agentic` (managed by the controller) shows lower p99 latency and lower 5xx error rate than `app-hpa` (managed by a standard K8s HPA scaling on CPU).

A pattern classifier observes each workload's long-run traffic history, identifies its shape, and automatically selects the parameters best suited to it — no operator intervention required. In production, different workloads exhibit different traffic patterns: a payment service is flat, an API gateway has a sharp periodic peak, a batch-triggered worker is bursty. No single set of cooldowns, step sizes, or forecasting models is optimal for all of them. The classifier runs as soon as the reconciler first sees a CR — within seconds, provided Prometheus already holds at least `CLASSIFIER_MIN_POINTS` of history for the target — and writes recommended params and long-term context to `status.classifiedParams`. (If history is short, classification waits for the next trigger; see §6.1.) The operator retains full override control per-field.

The cold path is a first-class contributor to forecasting, not just a parameter-tuning sidecar. Beyond cooldowns and step sizes, the classifier writes a compact context block — baseline RPS, p95 RPS, 24-element hourly profile, and 24-hour trend slope — that the hot path forwards to the Forecast Service on every call. Each forecaster uses this context to anchor its short-horizon prediction with long-horizon structure: Prophet adds the profile as an external regressor (which is what lets a 60-minute window predict an hourly cycle correctly); GBDT uses hour-of-day baseline as a feature; linear extrapolation blends its noisy 10-point recent slope with the cold-path 24h trend slope as a stability prior, and clips on p95 as a safety cap.

Every event that changes replicas (`scale_up`, `scale_down`, `step_capped_up`, `step_capped_down`) is followed asynchronously by a `scale_explained` K8s Event containing 2-3 sentences of plain English prose generated by a local Ollama LLM, explaining why the scaling decision was made using the actual traffic data, predicted RPS, classified pattern, long-term context, and effective parameters as context.

## **2\. Scope**

In scope

* Stateless Deployment only  
* Horizontal scaling (replicas) only  
* Two deployments under identical traffic — `app-agentic` (our controller) and `app-hpa` (standard HPA)  
* One Forecast Service running three forecasters: linear extrapolation, Prophet, and GBDT quantile (`gbdt_quantile`, p90); auto-selected by available history length and classifier-provided context; accepts optional `preferred_model` override hint  
* Cold path writes long-term context (baseline, p95, 24-element hourly profile, 24h slope) to `status.classifiedParams.context`; hot path forwards this context on every `/recommend` call  
* Target application instrumented with request duration histogram \+ status-labeled counter, with semaphore-bounded concurrency that returns 503 under overload  
* Kill switch via annotation  
* `maxStepSize` cap and explicit scale-up / scale-down cooldowns (classifier-derived by default, operator-overridable per-field)  
* HPA conflict detection — refuse to scale when another HPA already targets the same Deployment  
* Validating admission webhook — enforce CRD field bounds at apply time  
* Grafana dashboard with seven panels: actual vs predicted RPS, agentic replicas, sliding-window capacity per pod, scaling events, HPA replicas, p99 latency comparison, 5xx rate comparison  
* ClassifierWorker goroutine in the Controller — one per active CR, cold path only  
* Feature extraction over configurable Prometheus history: CV, peak-to-trough ratio (p99-based), hourly autocorrelation, trend slope  
* Five deterministic pattern classes: `flat`, `periodic`, `spiky`, `gradual_ramp`, `default`  
* Parameter formulae deriving cooldowns, `maxStepSize`, and preferred forecaster directly from extracted features; formula constants in the Controller  
* `status.classifiedParams` block written after each classification, including the new `context` sub-block  
* Optional spec override fields (`scaleUpCooldownSeconds`, `scaleDownCooldownSeconds`, `maxStepSize`, `preferredForecaster`)  
* Reconcile precedence: spec override \> `classifiedParams` \> env-var default (`context` is not part of the precedence chain — it is empirical data, not an operator preference; see §8)  
* Three re-classification triggers: periodic timer, manual annotation, Deployment rollout (revision annotation change)  
* Two new K8s event types from ClassifierWorker: `pattern_classified`, `pattern_unknown`  
* ExplainWorker goroutine in the Controller — one per active CR, async best-effort, triggered by any event that changes replicas  
* Ollama integration — local LLM via OpenAI-compatible endpoint; URL, model, timeout all configurable via env vars  
* One new K8s event type from ExplainWorker: `scale_explained` — prose annotation of each scale decision  
* All operational parameters configurable via env vars — timing intervals, history windows, thresholds, and reconcile defaults all have sensible defaults but can be overridden

Out of scope

* Multiple workloads beyond the two-Deployment side-by-side  
* Better forecaster history hygiene (Prophet fit caching)  
* Per-workload Forecast Service model caching/persistence (Forecast Service remains stateless; refits per call)  
* Vertical scaling, StatefulSets, Jobs, DaemonSets

## **3\. Architecture**

```
┌──────────────────────────────────────────────────────────────────┐
│                     Kubernetes Cluster (kind)                    │
│                                                                  │
│  ┌──────────────┐                                                │
│  │ app-hpa      │ ◄── managed by standard K8s HPA                │
│  │ Deployment   │       (target CPU utilization = 70%)            │
│  └──────┬───────┘                                                │
│         │ metrics                                                │
│         ▼                                                        │
│  ┌──────────────┐    ┌──────────────┐                            │
│  │ Prometheus   │ ◄──┤ app-agentic  │ ◄── managed by us           │
│  │              │    │ Deployment   │                            │
│  └──────┬───────┘    └──────▲───────┘                            │
│         │ PromQL            │ /scale subresource                 │
│         ▼                   │                                    │
│  ┌──────────────────────────────────────────────────────────┐    │
│  │ Controller (Go)                                          │    │
│  │                                                          │    │
│  │  Hot path  (every RECONCILE_INTERVAL_SECONDS per CR)     │    │
│  │  ┌────────────────────────────────────────────────────┐  │    │
│  │  │ Reconciler                                         │  │    │
│  │  │  resolves effective params (spec ?? classified ?? d)│  │    │
│  │  │  reads context from status.classifiedParams.context │  │    │
│  │  │  polls Prometheus, calls Forecast Service           │  │    │
│  │  │  patches /scale, emits events, updates status      │  │    │
│  │  └────────────────────────────────────────────────────┘  │    │
│  │                                                          │    │
│  │  Cold path (every CLASSIFIER_INTERVAL_MINUTES + triggers)│    │
│  │  ┌────────────────────────────────────────────────────┐  │    │
│  │  │ ClassifierWorker                                   │  │    │
│  │  │  queries Prometheus: CLASSIFIER_HISTORY_HOURS      │  │    │
│  │  │  extracts features, classifies pattern             │  │    │
│  │  │  computes params via formulae                      │  │    │
│  │  │  computes context (baseline, p95, profile, slope) │  │    │
│  │  │  writes status.classifiedParams (params + context) │  │    │
│  │  │  emits pattern_classified / pattern_unknown        │  │    │
│  │  └────────────────────────────────────────────────────┘  │    │
│  │                                                          │    │
│  │  Async best-effort (per CR, triggered by scale events)   │    │
│  │  ┌────────────────────────────────────────────────────┐  │    │
│  │  │ ExplainWorker                                      │  │    │
│  │  │  receives replica-change events via channel        │  │    │
│  │  │  constructs structured prompt (incl. context)      │  │    │
│  │  │  POST → Ollama (host, via host network)            │  │    │
│  │  │  emits scale_explained K8s Event (prose)           │  │    │
│  │  └────────────────────────────────────────────────────┘  │    │
│  └──────────────────────────┬───────────────────────────────┘    │
│                             │ HTTP /recommend                    │
│                             ▼                                    │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │ Forecast Service (Python/FastAPI)                          │  │
│  │  POST /recommend                                           │  │
│  │  { rps_history, workload_id, preferred_model?, context? }  │  │
│  │  Models: linear_extrap, prophet, gbdt_quantile             │  │
│  │  Auto-select rules: see §5.                                │  │
│  │  preferred_model overrides auto-selection if present.      │  │
│  │  context (when present) feeds every forecaster as anchor.  │  │
│  └────────────────────────────────────────────────────────────┘  │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │ k6 (two scenarios) — identical RPS to both targets         │  │
│  └────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
          │ ExplainWorker → POST /v1/chat/completions
          ▼
┌────────────────────────────────────────────────────────────────┐
│ Ollama (host process — not in-cluster)                         │
│  POST /v1/chat/completions                                     │
│  model: OLLAMA_MODEL (must be pre-pulled)                      │
└────────────────────────────────────────────────────────────────┘
```

Four Deployments: `app-hpa`, `app-agentic`, `controller`, `forecast-service`. Prometheus \+ Grafana from the standard `kube-prometheus-stack` Helm chart. The standard K8s HPA is a built-in resource — no separate Deployment. Ollama runs as a local process on the host (not in-cluster).

### **3.1 hot path vs. cold path**

The cold path writes two things to `status.classifiedParams`: the parameter knobs (cooldowns, maxStep, forecaster) and the context block (baseline, p95, hourly profile, slope). The hot path reads both: parameters drive policy; context is forwarded into the forecast request so long-term structure informs each short-term prediction.

## **4\. CRD: AgenticAutoscaler**

The CRD operates only against `app-agentic`. The HPA managing `app-hpa` is a standard K8s `HorizontalPodAutoscaler` resource and lives in `deploy/manifests/hpa.yaml`.

`apiVersion: autoscaling.agentic.io/v1alpha1`

```
apiVersion: autoscaling.agentic.io/v1alpha1
kind: AgenticAutoscaler
metadata:
  name: app-agentic
  namespace: demo
  annotations:
    autoscaling.agentic.io/kill-switch: "false"
    # autoscaling.agentic.io/reclassify: "true"   # set to trigger immediate re-classification;
    #                                               # removed by the controller after classification
    #                                               # succeeds; left in place if classification skips
    #                                               # (e.g. insufficient history) so operator can see
    #                                               # the request is still pending
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: app-agentic
  minReplicas: 2                 # optional; default 2
  maxReplicas: 10                # optional; default 10
  rpsPerPodMin: 50               # optional; default 50  (safety floor for self-calibrating rps_per_pod)
  rpsPerPodMax: 500              # optional; default 500 (safety ceiling)
  # Scaling behaviour fields below are optional. Omit to let the classifier auto-tune.
  # maxStepSize: 2
  # scaleUpCooldownSeconds: 30
  # scaleDownCooldownSeconds: 120
  # preferredForecaster: "linear_extrap"   # "auto" | "prophet" | "linear_extrap" | "gbdt_quantile"
status:
  phase: "Ready" | "Disabled" | "Conflict"
  conflictReason: ""             # populated only when phase=Conflict
  currentReplicas: 4
  recommendedReplicas: 6
  predictedRPS: 1200
  rpsPerPodCurrent: 187          # live sliding-window median value; persisted for restart recovery
  lastScaleTime: "2026-05-25T14:32:00Z"
  classifiedParams:              # written by ClassifierWorker; never written by reconciler
    pattern: "periodic"          # flat | periodic | spiky | gradual_ramp | default
    scaleUpCooldownSeconds: 60
    scaleDownCooldownSeconds: 300
    maxStepSize: 3
    preferredForecaster: "prophet"       # one of: linear_extrap | prophet | gbdt_quantile
    classifiedAt: "2026-05-25T14:00:00Z"
    historyPoints: 264               # 5-min buckets observed (~22h); near the 288 cap for a 24h window
    confidence: "high"               # "high" if historyPoints >= CLASSIFIER_HIGH_CONFIDENCE_POINTS, else "medium"
    context:                     # long-term forecasting context, written atomically with the rest
      baselineRPS: 850.0         # median over CLASSIFIER_HISTORY_HOURS window
      peakP95RPS: 2100.0         # p95 over CLASSIFIER_HISTORY_HOURS window
      trend24hSlope: 0.5         # rps/min, full-window least-squares slope
      hourlyProfile:             # length-24 array; UTC hour -> median RPS
        - 120.0
        - 100.0
        # ...22 more values...
      hourlyProfileValid: true   # false if < HOURLY_PROFILE_MIN_HOURS distinct hours covered
```

Operator annotations:

| Annotation | Values | Behaviour |
| ----- | ----- | ----- |
| `autoscaling.agentic.io/kill-switch` | `"true"` / `"false"` | `"true"` disables all scaling immediately; reconciler emits `kill_switched` and returns. Remove or set back to `"false"` to resume. Persists until the operator removes it. |
| `autoscaling.agentic.io/reclassify` | `"true"` | Triggers immediate re-classification outside the periodic timer — useful after a known traffic-pattern change (e.g. a major deploy or product launch). The controller removes the annotation after a successful classification; leaves it in place if classification skipped (e.g. insufficient history) so the operator can see the request is still pending. |

Optional spec fields: `minReplicas`, `maxReplicas`, `rpsPerPodMin`, `rpsPerPodMax`, `scaleUpCooldownSeconds`, `scaleDownCooldownSeconds`, `maxStepSize`, and `preferredForecaster` are all optional. Schema-level defaults (via kubebuilder markers) cover `minReplicas` (2), `maxReplicas` (10), `rpsPerPodMin` (50), and `rpsPerPodMax` (500). The scaling behaviour fields default to nil — "defer to classifier." A non-nil value is an explicit operator override for that field only — fields are independent.

`status.classifiedParams`: written by the ClassifierWorker after each successful classification. The only values ever written for `confidence` are `"high"` (≥ `CLASSIFIER_HIGH_CONFIDENCE_POINTS`) and `"medium"` (≥ `CLASSIFIER_MIN_POINTS`). Below `CLASSIFIER_MIN_POINTS` the worker emits `pattern_unknown` and skips the write entirely.

`status.classifiedParams.context`: written atomically with the rest of `classifiedParams`. The reconciler reads it verbatim and forwards it on each `/recommend` call. The fields are:

* `baselineRPS`: median RPS over the full classifier window  
* `peakP95RPS`: 95th-percentile RPS over the full classifier window — used by the Forecast Service as a safety cap on extrapolated predictions  
* `trend24hSlope`: least-squares slope (rps/min) over the full window — long-horizon trend  
* `hourlyProfile`: length-24 array indexed by UTC hour-of-day; each value is the median RPS observed in that hour across the full window. Hours with no data are stored as 0.0 (see `hourlyProfileValid` for whether this should be trusted)  
* `hourlyProfileValid`: `true` if at least `HOURLY_PROFILE_MIN_HOURS` distinct UTC hours had any observations; `false` otherwise. When `false`, downstream consumers ignore `hourlyProfile` and use other context fields only

Admission webhook: the controller registers a validating admission webhook for `AgenticAutoscaler` that rejects Create and Update requests when:

* `minReplicas < 1`  
* `maxReplicas < minReplicas` (no hard ceiling — the CRD default of 10 is a convenience, not a constraint)  
* `rpsPerPodMin < 1`  
* `rpsPerPodMin >= rpsPerPodMax`  
* `maxStepSize < 1` (if set)  
* `maxStepSize > (maxReplicas - minReplicas)` (if set)  
* `scaleUpCooldownSeconds < 0` (if set)  
* `scaleDownCooldownSeconds < 0` (if set)  
* `preferredForecaster` is set to any value other than `"prophet"`, `"linear_extrap"`, `"gbdt_quantile"`, or `"auto"`. Omitting the field and setting it to `"auto"` are equivalent — both fall through the precedence chain to the Forecast Service's auto-selection logic. `"auto"` is accepted for operators who want to record the explicit intent in the CR.

Optional fields fire their validation rules only when non-nil. Webhook TLS certificates are managed by cert-manager. Webhook URL is wired by kubebuilder scaffolding.

Why two bounds instead of a single `rpsPerPod`: the operator only commits to a range of plausible capacity per pod. The actual value is learned at runtime via a sliding-window median over steady-state samples (see §5 step 5). A wrong operator estimate does not propagate into wrong scaling decisions — the autoscaler self-corrects as it observes real load.

### **Configuration outside the CRD**

Most timing, history window, threshold, and LLM parameters are configurable via controller env vars. Everything has a sensible default; only `FORECAST_SERVICE_URL` and `PROMETHEUS_URL` are required. Two env vars (`FORECAST_HORIZON_MINUTES` and `PROPHET_MIN_POINTS`) must also be set on the Forecast Service deployment — see the note after the hot path timing table.

Infrastructure

| Env var | Default | What it controls |
| ----- | ----- | ----- |
| `FORECAST_SERVICE_URL` | (required) | Forecast Service endpoint |
| `PROMETHEUS_URL` | (required) | Prometheus endpoint |

Hot path timing

| Env var | Default | What it controls |
| ----- | ----- | ----- |
| `RECONCILE_INTERVAL_SECONDS` | 60 | How often the reconciler runs per CR |
| `HOT_PATH_HISTORY_MINUTES` | 60 | RPS history window sent to Forecast Service |
| `HOT_PATH_MIN_POINTS` | 10 | Minimum data points before hot path can act |
| `FORECAST_HORIZON_MINUTES` | 10 | How far ahead the Forecast Service predicts |
| `FORECAST_TIMEOUT_SECONDS` | 5 | HTTP timeout for Forecast Service calls |
| `PROPHET_MIN_POINTS` | 30 | History points needed to select Prophet over linear\_extrap in `auto` mode |
| `RPS_PER_POD_NOISE_FLOOR_RPS` | 10 | Minimum `current_rps` required before a steady-state `current_rps / current_replicas` observation is pushed to the per-CR ring buffer (see §5 step 5). Below this floor, the ratio is dominated by sampling noise and would corrupt the median; we hold the previous `rps_per_pod` estimate instead. Operators with intentionally-low-RPS workloads (e.g., < 10 rps under load) should lower this to match. |

Note: `FORECAST_HORIZON_MINUTES` and `PROPHET_MIN_POINTS` control logic inside the Forecast Service (Python), not the controller. Both env vars must be set on the Forecast Service deployment as well as the controller — keeping them in sync is the operator's responsibility. `RECONCILE_INTERVAL_SECONDS`, `HOT_PATH_HISTORY_MINUTES`, `HOT_PATH_MIN_POINTS`, `FORECAST_TIMEOUT_SECONDS`, and `RPS_PER_POD_NOISE_FLOOR_RPS` are controller-only. The Forecast Service-only env vars (`GBDT_QUANTILE`, `GBDT_MIN_POINTS`, `PROPHET_USE_HOURLY_REGRESSOR`, `LINEAR_EXTRAP_TREND_BLEND`) are listed in the next table and need only be set on the Forecast Service deployment.

Cold path timing

| Env var | Default | What it controls |
| ----- | ----- | ----- |
| `CLASSIFIER_INTERVAL_MINUTES` | 30 | Re-classification cadence |
| `CLASSIFIER_HISTORY_HOURS` | 24 | History window queried from Prometheus for classification |
| `CLASSIFIER_MIN_POINTS` | 72 | Minimum points before classification runs (also sets "medium" confidence floor). At the default 5-min resolution this is \~6h of history — long enough that `cv`, `peak_to_trough` (p99-based), and `hourly_autocorr` are all statistically meaningful. The constraint `CLASSIFIER_MIN_POINTS >= hourly_lag_points + 10` must always hold (see §7); 72 ≥ 12 + 10 = 22 satisfies it comfortably. |
| `CLASSIFIER_HIGH_CONFIDENCE_POINTS` | 240 | Points required for `confidence: "high"`. At the default 5-min resolution this is \~20h of history — nearly the full classifier window, ensuring `hourlyProfileValid` is essentially guaranteed to be `true` at this point. |
| `CLASSIFIER_DEDUP_SECONDS` | 60 | Suppresses redundant rollout-triggered re-classifications (collapses the initial-sync race with the immediate-first-run trigger). |
| `HOURLY_PROFILE_MIN_HOURS` | 12 | Distinct UTC hours of history required to mark `hourlyProfileValid: true` |
| `CONTEXT_DOWNSAMPLE_RESOLUTION_MIN` | 5 | Resolution used when computing the 24h context series (5-min buckets, not 1-min, keeps the cold path cheap). All point-count thresholds above are sized for this default; if you change the resolution, rescale `CLASSIFIER_MIN_POINTS` and `CLASSIFIER_HIGH_CONFIDENCE_POINTS` together. |
| `CV_GUARD_MEAN_RPS` | 1.0 | Below this mean RPS, the classifier's `cv` feature is forced to 0 (the workload is treated as effectively idle and CV is meaningless). Tuned for typical-traffic workloads; lower it if you operate sub-1-RPS workloads where the classifier should still extract variance, or raise it if your workload's "noise floor" is much higher. Operator-tunable because it depends on the workload's RPS scale. |

Pre-classification reconcile defaults (used before the classifier has written `classifiedParams`; superseded once classification runs)

| Env var | Default | What it controls |
| ----- | ----- | ----- |
| `DEFAULT_SCALE_UP_COOLDOWN_SECONDS` | 60 | Fallback scale-up cooldown |
| `DEFAULT_SCALE_DOWN_COOLDOWN_SECONDS` | 300 | Fallback scale-down cooldown |
| `DEFAULT_MAX_STEP_SIZE` | 4 | Fallback max replicas moved per reconcile |

Forecast Service model parameters (set on Forecast Service deployment)

| Env var | Default | What it controls |
| ----- | ----- | ----- |
| `GBDT_QUANTILE` | 0.90 | Upper quantile predicted by `gbdt_quantile` (p90 \= scale for a worse-than-typical burst) |
| `GBDT_MIN_POINTS` | 30 | Precondition for `forecast_gbdt_quantile`: if `len(rps_history) < GBDT_MIN_POINTS`, the path falls back to `forecast_linear_extrap`. Mirrors `PROPHET_MIN_POINTS`; both default to 30 because a few dozen samples is the rough lower bound for either model to fit something more honest than a line. |
| `PROPHET_USE_HOURLY_REGRESSOR` | `true` | When true and `context.hourly_profile_valid` is true, Prophet adds `hour_baseline` as an external regressor. Operators who want to test Prophet without the seasonal prior can set this to `false`. |
| `LINEAR_EXTRAP_TREND_BLEND` | `0.7` | Blend weight in `forecast_linear_extrap` between the recent-window slope (weight α) and `context.trend_24h_slope` (weight 1-α). Set to `1.0` to disable the blend (recent slope only); set lower to lean more on the long-horizon trend. Only applies when `context.trend_24h_slope` is present. |

Ollama (ExplainWorker)

| Env var | Default | What it controls |
| ----- | ----- | ----- |
| `OLLAMA_URL` | `http://localhost:11434` | Ollama server address |
| `OLLAMA_MODEL` | `llama3.2` | Model used for scale explanations (must be pre-pulled with `ollama pull llama3.2`) |
| `OLLAMA_TIMEOUT_SECONDS` | 30 | Request timeout per Ollama call |
| `OLLAMA_MAX_TOKENS` | 150 | Maximum tokens in the explanation response |

Hardcoded — PromQL templates, formula constants, classification thresholds, and per-forecaster safety-cap multipliers live in source. They are algorithm internals, not operational knobs.

## **5\. Hot path behavior**

The controller reconciles every `RECONCILE_INTERVAL_SECONDS` per `AgenticAutoscaler` CR.

### **Reconcile loop**

Preamble — resolve effective params and context (before any step below; see §8 for full precedence rules):

```
effectiveCooldownUp   = spec.scaleUpCooldownSeconds   ?? classifiedParams.scaleUpCooldownSeconds   ?? DEFAULT_SCALE_UP_COOLDOWN_SECONDS
effectiveCooldownDown = spec.scaleDownCooldownSeconds  ?? classifiedParams.scaleDownCooldownSeconds  ?? DEFAULT_SCALE_DOWN_COOLDOWN_SECONDS
effectiveMaxStep      = spec.maxStepSize               ?? classifiedParams.maxStepSize               ?? DEFAULT_MAX_STEP_SIZE
effectiveForecaster   = spec.preferredForecaster       ?? classifiedParams.preferredForecaster       ?? "auto"
effectiveContext      = status.classifiedParams.context   // may be nil on cold start; not part of precedence chain
```

Note on default values vs. classifier neutral output. The pre-classification fallbacks (`DEFAULT_SCALE_UP_COOLDOWN_SECONDS` / `DEFAULT_SCALE_DOWN_COOLDOWN_SECONDS` / `DEFAULT_MAX_STEP_SIZE`, defaulting to 60s/300s/4) are intentionally more reactive than what the classifier produces on flat traffic (which yields 120s/180s/1 at cv=0, hourly\_autocorr=0). The rationale: without classification we know nothing about the workload, so we err toward faster scale-up, slower scale-down (to avoid yo-yoing on noise), and a slightly larger step size to recover from underprovision quickly. Once the classifier runs, its data-driven recommendation supersedes these defaults.

Note on `effectiveContext`. There is no `spec.context` override. Context is empirical data extracted from the cluster, not an operator preference. The reconciler reads `status.classifiedParams.context` verbatim, or omits the `context` field in `/recommend` entirely if the classifier has not yet written one (cold start).

1. Pre-checks (short-circuit on first match):  
   * `autoscaling.agentic.io/kill-switch=true` → set `status.phase = "Disabled"`, emit `kill_switched` event, return. Remove the annotation (or set it to `"false"`) to restore normal reconciliation.  
   * CR being deleted → return.  
   * HPA conflict check. List all HPAs in the CR's namespace. If any HPA's `spec.scaleTargetRef` matches our `spec.targetRef` (same kind \+ name): set `status.phase = "Conflict"`, set `status.conflictReason = "HPA <hpa-name> already manages this Deployment"`, emit `conflict_detected` event, return. The conflict clears automatically on the next reconcile after the offending HPA is deleted.  
2. Query Prometheus:  
   * `current_rps = sum(rate(http_requests_total{deployment="<target>"}[2m]))`  
   * `current_replicas` from Deployment status  
   * `rps_history`: last `HOT_PATH_HISTORY_MINUTES` at 1-minute resolution. If Prometheus has fewer points, use whatever is available, down to a minimum of `HOT_PATH_MIN_POINTS`; if there are fewer than `HOT_PATH_MIN_POINTS`, emit `metrics_unavailable` and no-op for this cycle.  
3. POST to Forecast Service:

```
   POST /recommend
   Content-Type: application/json
   {
     "rps_history": [...up to HOT_PATH_HISTORY_MINUTES values...],
     "workload_id": "<namespace>/<name>",
     "preferred_model": "<effectiveForecaster, omitted if 'auto'>",
     "context": {                                 // omitted entirely if effectiveContext is nil
       "baseline_rps": 850.0,
       "peak_p95_rps": 2100.0,
       "trend_24h_slope": 0.5,
       "hourly_profile": [120.0, 100.0, /* ...24 values... */],
       "hourly_profile_valid": true,
       "current_hour_utc": 14,                    // computed by the reconciler at request time
       "current_minute_utc": 30                   // ditto; pins Prophet/GBDT timestamp anchor exactly
     }
   }
   
```

Timeout: `FORECAST_TIMEOUT_SECONDS` (default: 5s). The `current_hour_utc` and `current_minute_utc` fields are computed by the reconciler at request time so the Forecast Service does not need its own clock semantics (the controller's clock is the source of truth). Together they pin the Prophet/GBDT timestamp anchor exactly to "now" — without `current_minute_utc`, the up-to-59-minute drift between the anchor and the real timestamp of `rps_history[-1]` mislabels roughly half the training rows by one hour and silently neutralises the hourly regressor. Both fields are sent on every request but are **not persisted** in `status.classifiedParams.context` — they are request-scoped derived data, unlike the rest of the context block.

4. Receive response:

```
   {
     "predicted_rps": 1450,
     "horizon_minutes": 10,
     "model_used": "prophet"
   }
   
```

`model_used` is one of `"linear_extrap"`, `"prophet"`, `"gbdt_quantile"`.

5. Compute `target_replicas` in the Controller:  
   The Controller maintains a per-CR in-memory ring buffer of up to 10 steady-state `rps_per_pod` observations, keyed by `namespace/name`. The current estimate is the median of that buffer.  
    Restart recovery. Both `status.rpsPerPodCurrent` and `status.lastScaleTime` are written to the CR after every reconcile (step 11). On the first reconcile after a Controller restart, in-memory state is seeded from those fields so the steady-state gate and cooldown enforcement (step 7\) both survive the restart. If `rpsPerPodCurrent` is absent or out of bounds, `rps_per_pod` falls back to the midpoint `(spec.rpsPerPodMin + spec.rpsPerPodMax) / 2`. The observations ring buffer is also seeded with the persisted value (when available) so that the first new steady-state observation produces a 2-element median rather than overwriting the persisted value with a single point.

```
   # First-time-seeing-this-CR initialisation (also runs once after a restart):
   if first time seeing this CR:
       if status.rpsPerPodCurrent set and in [spec.rpsPerPodMin, spec.rpsPerPodMax]:
           rps_per_pod := status.rpsPerPodCurrent
           # Seed the buffer with 5 copies of the persisted value (half of
           # capacity 10) so the persisted estimate genuinely keeps weight
           # across the next few new observations: median([persisted]*5,
           # new1) = persisted, median([persisted]*5, new1, new2) = persisted,
           # … only after ≥6 fresh observations has the persisted seed been
           # outvoted. A single-element seed (the v2-draft) was washed out
           # by the second new observation and did not preserve the
           # persisted value as intended.
           observations := [status.rpsPerPodCurrent] * 5
       else:
           rps_per_pod := (spec.rpsPerPodMin + spec.rpsPerPodMax) / 2
           observations := []                          # empty ring buffer, capacity 10
       # Seed cooldown timers from the persisted status.lastScaleTime so a
       # restart cannot accidentally bypass cooldowns. Both directions get the
       # same seed value — conservative; errs on the side of waiting.
       if status.lastScaleTime is set:
           lastScaleUpTime   := status.lastScaleTime
           lastScaleDownTime := status.lastScaleTime
       else:
           lastScaleUpTime, lastScaleDownTime := zero (treated as "long ago")

   # Steady-state gate: skip the rps_per_pod update if a scale event in either
   # direction occurred within the last 2 reconcile intervals. Avoids
   # contaminating the buffer with transient rps/replica ratios observed while
   # new pods are still starting or load is still redistributing.
   lastScale := max(lastScaleUpTime, lastScaleDownTime)
   if current_rps >= RPS_PER_POD_NOISE_FLOOR_RPS and current_replicas >= 1:
       if time.Since(lastScale) >= 2 * RECONCILE_INTERVAL_SECONDS:
           observations.push(current_rps / current_replicas)   # evicts oldest if full
           rps_per_pod := median(observations)
   # Otherwise: keep the previous rps_per_pod value.

   rps_per_pod := clamp(rps_per_pod, spec.rpsPerPodMin, spec.rpsPerPodMax)

   # Two-step recommendation:
   #   unboundedRecommended is the raw forecaster output, before any limit.
   #   recommendedReplicas (the value published to status) clamps it to the
   #   CRD's [minReplicas, maxReplicas] bounds.
   # We compare the two so the operator can see when maxReplicas (or
   # minReplicas) is the binding constraint — otherwise an over-provisioned
   # forecast silently disappears into the clamp and the operator sees
   # "everything fine" while the workload is actually under-provisioned.
   unboundedRecommended := ceil(predicted_rps / rps_per_pod)
   recommendedReplicas  := clamp(unboundedRecommended,
                                 spec.minReplicas, spec.maxReplicas)
   target := recommendedReplicas

   if unboundedRecommended > spec.maxReplicas:
       # Tentative reasoning token; step 7's cooldown check or step 8's
       # hysteresis guard may overwrite this if a downstream constraint
       # also fires.
       reasoning := "max_replicas_binding"
   elif unboundedRecommended < spec.minReplicas:
       reasoning := "min_replicas_binding"
   
```

`recommendedReplicas` semantics. The post-CRD-bounds, pre-step-cap, pre-cooldown recommendation. It answers "given the forecast and the CRD's `[minReplicas, maxReplicas]` bounds, what's the right replica count?" — but **not** "what would the forecaster have asked for if there were no CRD bounds." When `unboundedRecommended` exceeds `maxReplicas`, the new `max_replicas_binding` reasoning token (emitted in step 10) makes that visible to the operator; otherwise the recommendation matches `clamp(forecast, minReplicas, maxReplicas)` and the gap between `recommendedReplicas` and `currentReplicas` reflects only step-cap and cooldown constraints (which the existing `step_capped_*` and `cooldown_holding_*` tokens cover). The actually-patched value (after step 6 cap and step 7 cooldown) is observable via the Deployment's replica count and the emitted K8s Event message.

The ring buffer fills in at most 10 steady-state reconciles. The median is insensitive to individual overload spikes. The `rps_per_pod` clamp is a last-resort guard against pathological observations.

This math is in the Controller, not the Forecast Service — the Forecast Service is a pure forecaster and knows nothing about pods or replicas. Long-term context enters via `predicted_rps`, not via the divisor.

6. Apply `maxStepSize` cap. Limit how far `target` can move in a single reconcile using `effectiveMaxStep`:

```
   if target > current_replicas:
       target := min(target, current_replicas + effectiveMaxStep)
   elif target < current_replicas:
       target := max(target, current_replicas - effectiveMaxStep)
   
```

If the cap was hit, tentatively record `step_capped_up` or `step_capped_down` as the reasoning token. Step 7 may overwrite this if cooldown also blocks.

7. Apply cooldowns. Controller maintains per-CR in-memory `lastScaleUpTime` and `lastScaleDownTime` (seeded from `status.lastScaleTime` on restart per step 5, or "long ago" on a never-seen CR). Uses `effectiveCooldownUp` and `effectiveCooldownDown`:

```
   now := time.Now()
   if target > current_replicas:
       if now - lastScaleUpTime < effectiveCooldownUp:
           target := current_replicas               # block scale-up
           reasoning := "cooldown_holding_up"       # overrides step_capped_up if also set
       else:
           lastScaleUpTime := now
   elif target < current_replicas:
       if now - lastScaleDownTime < effectiveCooldownDown:
           target := current_replicas               # block scale-down
           reasoning := "cooldown_holding_down"     # overrides step_capped_down if also set
       else:
           lastScaleDownTime := now
   
```

Reasoning-token precedence (when multiple constraints fire in the same reconcile):

1. **Step 5** tentatively sets `max_replicas_binding` or `min_replicas_binding` when the forecaster's unbounded recommendation falls outside the CRD bounds.
2. **Step 6** overwrites with `step_capped_up` / `step_capped_down` when the maxStep cap clips the move (because the immediate visible constraint is the cap, not the CRD bound — the cap is what the operator can act on by raising `spec.maxStepSize`). The CRD-bound information is still present implicitly: when both fire, `recommendedReplicas` will equal `spec.maxReplicas` (or `spec.minReplicas`) and the operator can compare against `unboundedRecommended` in the event message.
3. **Step 7** overwrites with `cooldown_holding_*` when cooldown blocks the move entirely. `cooldown_holding_*` wins over `step_capped_*` because the final outcome is no replica change — and `step_capped_*` is reserved for events that do change replicas.
4. **Step 8** hysteresis suppresses event emission entirely when `target == current_replicas` after all prior steps. ExplainWorker is therefore not triggered in cooldown-blocked cases.

A `max_replicas_binding` event without a `step_capped_*` or `cooldown_holding_*` override means the workload is at maxReplicas and the forecaster wanted more — operators should treat this as a capacity-planning signal regardless of whether replicas changed this reconcile.

8. Hysteresis: only patch if `target != current_replicas`.  
9. Patch `/scale`: patch the target Deployment's `/scale` subresource to `target`.  
10. Emit a K8s Event with a reasoning token:  
    * \`scale\_up\` / \`scale\_down\` / \`no\_change\` (normal cases)  
      * `step_capped_up` / `step_capped_down` (cap clipped the target)  
      * `cooldown_holding_up` / `cooldown_holding_down` (cooldown blocked the scale)  
      * `max_replicas_binding` / `min_replicas_binding` (CRD bound is the binding constraint — the forecast asked for more/fewer replicas than `[spec.minReplicas, spec.maxReplicas]` permits. Step 6 cap and step 7 cooldown may still override this token if they also fire.)  
      * `kill_switched` (pre-check)  
      * `conflict_detected` (HPA conflict pre-check)  
      * `forecast_unavailable` (see §9)  
      * `metrics_unavailable` (see §9)  
    * Event message includes `current_rps`, `predicted_rps`, `current_replicas`, `target`, `recommendedReplicas`, `unboundedRecommended` (when it differs from `recommendedReplicas`), `model_used`, and the effective params used in this reconcile. The `unboundedRecommended` field surfaces capacity-planning signals: when it exceeds `spec.maxReplicas`, the operator can see exactly how much capacity the forecast asked for vs. how much the CRD bound permits, regardless of whether replicas changed this reconcile. After emitting any replica-changing event (`scale_up`, `scale_down`, `step_capped_up`, `step_capped_down`, or `max_replicas_binding` / `min_replicas_binding` *when replicas changed*): perform a drop-and-replace send to the ExplainWorker's buffered channel — see §6.2 for the channel semantics. The reconcile loop never waits on this send.  
11. Update CR status (`currentReplicas`, `recommendedReplicas`, `predictedRPS`, `rpsPerPodCurrent`, `lastScaleTime`, `phase`, `conflictReason` if applicable). `status.recommendedReplicas` is the pre-cap, pre-cooldown value computed in step 5 (the `recommendedReplicas` local variable), not the post-cap target that step 6 mutates and step 9 patches. Publishing the unclipped recommendation is what lets the operator see "we wanted N but caps/cooldowns are preventing it." `lastScaleTime` is written as `max(lastScaleUpTime, lastScaleDownTime)` — the most recent scale event in either direction. The reconciler does not write `classifiedParams` — that field is owned exclusively by the ClassifierWorker.

### **Forecast Service `/recommend` pipeline**

Input: `{ rps_history: [non-empty array of non-negative floats; controller sends up to HOT_PATH_HISTORY_MINUTES values], workload_id: str (optional, accepted but unused), preferred_model: str (optional), context: object (optional) }`

`context` fields (all required when `context` is present):

* `baseline_rps: float` (\>= 0\)  
* `peak_p95_rps: float` (\>= 0\)  
* `trend_24h_slope: float` (rps/min — see §6.1 step 6.5 for the unit convention)  
* `hourly_profile: array of 24 floats` (\>= 0 each)  
* `hourly_profile_valid: bool`  
* `current_hour_utc: int` (0..23)  
* `current_minute_utc: int` (0..59)

Output: `{ predicted_rps: float, horizon_minutes: int, model_used: str }`

```
1. Validate: len(rps_history) >= 1, all values >= 0. Reject with 400 otherwise.
   If context is present, validate field types/ranges. Specifically,
   current_hour_utc must be an int in [0, 23] and current_minute_utc must
   be an int in [0, 59]; values outside these ranges (or missing required
   context fields) are treated as malformed context. On any context-
   validation failure, drop context (treat as if absent) and log a
   warning. Do not 400 on bad context — the hot path must keep working.
   (The controller enforces HOT_PATH_MIN_POINTS before calling; the service
   does not re-validate against controller-internal env vars.)

2. Resolve model:
       MODELS = {"linear_extrap", "prophet", "gbdt_quantile"}
       requested = preferred_model if preferred_model in MODELS else "auto"

       if requested in {"prophet", "gbdt_quantile"}:
           try:
               return dispatch[requested](rps_history, context)
           except Exception as e:
               log.warning(f"{requested} failed: {e}; falling back to linear_extrap")
               return forecast_linear_extrap(rps_history, context)

       if requested == "linear_extrap":
           return forecast_linear_extrap(rps_history, context)

       # requested == "auto"
       if len(rps_history) >= PROPHET_MIN_POINTS:
           try:
               return forecast_prophet(rps_history, context)
           except Exception as e:
               log.warning(f"Prophet failed: {e}; falling back to linear_extrap")
       return forecast_linear_extrap(rps_history, context)

3. Clamp predicted_rps to >= 0 in every implementation.

4. Return { predicted_rps, horizon_minutes: FORECAST_HORIZON_MINUTES, model_used }
```

`preferred_model` absent, null, or `"auto"` leaves auto-selection untouched. All three forecasters accept `(rps_history, context)` and return the same response shape. `context` may be `None`.

Note: `auto` mode never returns `gbdt_quantile`. Auto-selection only ever picks Prophet (when `len(rps_history) >= PROPHET_MIN_POINTS`) or falls back to linear extrapolation. The GBDT path is intentionally opt-in — it runs only when the classifier writes `preferredForecaster: "gbdt_quantile"` (driven by `pattern == "spiky"`; see §7) or when an operator explicitly sets `spec.preferredForecaster: "gbdt_quantile"` on the CR. This keeps the cold-path classifier in charge of when "spiky workload, predict-the-burst" semantics apply, rather than letting the Forecast Service infer it from `rps_history` alone.

### **forecast\_linear\_extrap**

```
1. Take the last min(10, len(rps_history)) points of rps_history.
2. Fit a least-squares line y = m*x + b (x = minute indices 0..n-1, y = rps).
3. If context is not None and context.trend_24h_slope is not None:
       # Blend the noisy 10-point recent slope with the long-horizon
       # 24h slope as a stability prior. Both are in rps/min.
       m = LINEAR_EXTRAP_TREND_BLEND * m
         + (1 - LINEAR_EXTRAP_TREND_BLEND) * context.trend_24h_slope
       # Default LINEAR_EXTRAP_TREND_BLEND = 0.7 — favor the recent
       # window (it carries the operative signal for the next few
       # minutes) while pulling toward the long-term direction when
       # the recent slope is dominated by sample noise.
       #
       # CRITICAL: recompute b so the line still passes through the
       # data's centroid (mean(x), mean(y)). In least squares, m and b
       # are jointly determined; rotating the line around x=0 by
       # changing m alone biases predictions by
       # (m_new - m_original) * (n-1)/2 RPS at horizon. Recomputing b
       # rotates around the centroid instead, which is the correct
       # behaviour for a slope adjustment.
       b = mean(y) - m * mean(x)
4. Extrapolate to x = n + (FORECAST_HORIZON_MINUTES - 1)
       predicted_rps = m * (n + FORECAST_HORIZON_MINUTES - 1) + b
5. predicted_rps = max(0, predicted_rps)
6. If context is not None:
       safety_cap = context.peak_p95_rps * 1.5
       predicted_rps = min(predicted_rps, safety_cap)
7. Return { predicted_rps, horizon_minutes: FORECAST_HORIZON_MINUTES,
            model_used: "linear_extrap" }
```

Rationale for the trend blend: a 10-point least-squares slope on noisy 1-min data can swing wildly turn-to-turn — a single high or low sample at the boundary materially changes `m`. Blending in the cold path's `trend_24h_slope` (computed once per classifier run from a smoothed 24h series) damps this without erasing the recent signal that matters most for short-horizon prediction. When the long-horizon trend agrees with the recent slope, the blend is a no-op; when they disagree (e.g., recent noise vs. a steady long-term direction), the prediction is pulled toward the more reliable estimate.

Rationale for the context cap: even after blending, a steep linear ramp on noisy 60-minute data can extrapolate to absurd values. The cold path's p95 is a sane upper bound; allow 1.5x for legitimate spikes above the typical p95, but clip anything beyond.

### **forecast\_prophet**

```
1. Build a DataFrame, anchoring timestamps to the controller's clock:
       if context is present:
           # Construct an anchor whose hour-of-day AND minute-of-hour equal
           # the controller's wall-clock at request time. Calendar date is
           # arbitrary — Prophet has weekly/daily seasonality disabled, so
           # only the hour and minute components matter. This pins ds to
           # real time exactly, so utc_hour_of(ds[i]) is correct for every
           # training row (without current_minute_utc, up to 30 of 60 1-min
           # rows would be mislabeled to the wrong UTC hour).
           ds_anchor = pd.Timestamp(year=2000, month=1, day=2,
                                    hour=context.current_hour_utc,
                                    minute=context.current_minute_utc)
       else:
           ds_anchor = pd.Timestamp.utcnow()
           # only used to make ds monotonically increasing — no regressor
           # will be added in this branch, so the absolute hour/minute do
           # not matter

       ds = pd.date_range(end=ds_anchor, periods=len(rps_history), freq="1min")
       y  = rps_history values
   The Forecast Service does NOT call its own clock for ds construction
   when context is present — the controller's (current_hour_utc,
   current_minute_utc) pair is the single source of truth for ds
   alignment.

2. If context is not None and context.hourly_profile_valid and PROPHET_USE_HOURLY_REGRESSOR:
       # Compute the "expected hour-of-day baseline" for each ds, looked up
       # from context.hourly_profile by UTC hour. Because ds is anchored to
       # context.current_hour_utc, utc_hour_of(t) for t in ds is well-defined
       # without depending on the service's wall clock.
       df["hour_baseline"] = [context.hourly_profile[utc_hour_of(t)] for t in ds]
       use_regressor = True
   else:
       use_regressor = False

3. Fit:
       m = Prophet(daily_seasonality=False, weekly_seasonality=False,
                   changepoint_prior_scale=0.5)
       if use_regressor:
           m.add_regressor("hour_baseline")
       m.fit(df)
   (Daily/weekly seasonality disabled — no multi-day history available from
   rps_history; the hourly regressor injects the seasonal level the cold
   path observed. Prophet's trend + changepoint detection is what beats
   linear extrapolation on plateaus and curving ramps.)

4. Build a future DataFrame extending FORECAST_HORIZON_MINUTES past the last ds.
   If use_regressor, fill hour_baseline values for the future timestamps too
   (look up each future ds's UTC hour in context.hourly_profile). Future
   ds inherit the controller-anchored offset from step 1.

5. predicted_rps = model.predict(future).iloc[-1].yhat
6. predicted_rps = max(0, predicted_rps)
   If context is not None:
       predicted_rps = min(predicted_rps, context.peak_p95_rps * 2.0)
7. Return { predicted_rps, horizon_minutes: FORECAST_HORIZON_MINUTES,
            model_used: "prophet" }
```

The hourly regressor gives Prophet a strong seasonal prior derived from 24h of history, which it cannot infer from a 60-minute window alone. Prophet's changepoint detection still operates on the 60-minute trend; the regressor only sets the level. Without the regressor, Prophet on a 60-minute window collapses toward linear extrapolation. With the v2 audit's removal of `forecast_hw_seasonal`, this Prophet+regressor path is the **sole** forecaster that captures hourly periodicity — `periodic + hourlyProfileValid` and `periodic + !hourlyProfileValid` both map here (in the latter case `use_regressor` is false and Prophet falls back on its trend/changepoint mechanism alone).

Why `PROPHET_MIN_POINTS` is 30 in `auto` mode: Prophet wants enough data to identify trend changepoints. With fewer than \~30 1-min points it tends to either overfit or produce flat predictions; linear extrapolation is more honest in that regime. The previous v2-draft value of 60 was a knife-edge against the default `HOT_PATH_HISTORY_MINUTES=60`, meaning Prophet only engaged when the full window was available; lowering to 30 lets Prophet engage halfway through the warm-up period.

### **forecast\_gbdt\_quantile**

Gradient-boosted regression with quantile loss, predicting the upper quantile (default p90, configurable via `GBDT_QUANTILE`). Targets spiky workloads where the mean prediction is misleading. Uses `sklearn.ensemble.GradientBoostingRegressor(loss="quantile", alpha=GBDT_QUANTILE)`.

```
Preconditions:
  - len(rps_history) >= GBDT_MIN_POINTS  (default 30 — GBDT needs at least a few dozen samples)

1. If preconditions fail:
       return forecast_linear_extrap(rps_history, context)

2. Build the same anchored ds array as forecast_prophet step 1 (using
   context.current_hour_utc + context.current_minute_utc when context is
   present, else the service's own clock). All time-derived features
   below are computed from ds[t], not from the service's own clock — so
   they share Prophet's exact-minute alignment guarantee.

3. Feature engineering — for each index t in the series, build features:
       lag_1, lag_2, lag_5, lag_10                   (recent values)
       rolling_mean_5, rolling_max_5, rolling_std_5  (recent volatility)
       hour_of_day_baseline                          (= context.hourly_profile[utc_hour_of(ds[t])] if context.hourly_profile_valid, else 0)
       minute_in_hour                                (= ds[t].minute, 0..59)
       trend_slope_local                             (slope of last 10 points up to t)

4. Target: y[t + FORECAST_HORIZON_MINUTES] for the training rows.
   (Standard sliding-window supervised setup. Training rows are the
   first n - FORECAST_HORIZON_MINUTES indices.)

5. Fit:
       GradientBoostingRegressor(loss="quantile", alpha=GBDT_QUANTILE,
                                 n_estimators=50, max_depth=3,
                                 learning_rate=0.1, random_state=0).fit(X, y_target)

6. Predict on the last row's features (features computed at t = n-1,
   forecasting t = n-1 + FORECAST_HORIZON_MINUTES).

7. predicted_rps = max(0, prediction)
   If context is not None:
       predicted_rps = min(predicted_rps, context.peak_p95_rps * 3.0)
   (Looser cap than the other models because spiky workloads legitimately
   exceed p95 — that's the whole point of using p90 quantile loss here.)

8. Return { predicted_rps, horizon_minutes: FORECAST_HORIZON_MINUTES,
            model_used: "gbdt_quantile" }
```

Why GBDT quantile instead of mean: for spiky workloads, the mean prediction under-provisions (it averages quiet minutes against burst minutes). The upper-quantile prediction asks "what is a worse-than-typical near-future value?" — which is the right anchor for capacity planning. This is the technique used by production predictive scalers for irregular load.

Why `n_estimators=50, max_depth=3`: small model, fits in tens of milliseconds, won't overfit a 60-point series. Trades sophistication for predictability.

### **Warm-up**

At FastAPI startup (`@app.on_event("startup")`), the service performs one dummy fit of each non-trivial model on a small synthetic series so the first real `/recommend` call doesn't pay the import/compile/Stan-compilation cost. Without warm-up, the first call may exceed the Controller's `FORECAST_TIMEOUT_SECONDS` timeout. Order: cheapest to most expensive — GBDT → Prophet.

### **Per-forecaster safety-cap multipliers (hardcoded)**

| Model | Multiplier on `peak_p95_rps` |
| ----- | ----- |
| `linear_extrap` | 1.5x |
| `prophet` | 2.0x |
| `gbdt_quantile` | 3.0x (looser; the model's whole purpose is to predict above p95) |

These multipliers are formula constants, not operator knobs. They live in Forecast Service source.

## **6\. Cold-path workers**

Two goroutines run per active `AgenticAutoscaler` CR alongside the hot-path reconciler. Both are started when the reconciler first sees a CR and stopped (via context cancellation) when the CR is deleted. Neither blocks the reconcile loop.

### **6.1 ClassifierWorker**

#### **Triggers**

1. Immediate first run — when the reconciler first sees a CR, the worker runs classification once before starting its periodic timer. If fewer than `CLASSIFIER_MIN_POINTS` history points exist in Prometheus, it emits `pattern_unknown` and waits for the next trigger; otherwise the CR reaches its classified state without waiting up to `CLASSIFIER_INTERVAL_MINUTES` for the first timer tick.  
2. Periodic timer — fires every `CLASSIFIER_INTERVAL_MINUTES` minutes (default 30\) after the immediate first run.  
3. Manual annotation — operator sets `autoscaling.agentic.io/reclassify: "true"` on the CR. A controller-runtime watcher on `AgenticAutoscaler` observes the annotation change and signals the worker to run classification immediately; the worker then removes the annotation via a patch. Intended for use after a known traffic-pattern change (e.g., a major deploy or a product launch).  
4. Deployment rollout — a controller-runtime watcher (informer) on the target Deployment observes changes to the `deployment.kubernetes.io/revision` annotation and signals the worker to re-classify immediately, since new code often changes traffic characteristics. The revision annotation is incremented by the Deployment controller only on actual rollouts (image / env / command changes that produce a new ReplicaSet) — **not** on `/scale` patches. We deliberately do **not** watch `metadata.generation`, because the apiserver bumps that field on every `spec.replicas` update too, which would cause this trigger to fire on every reconcile that scales — defeating the purpose. Dedup: the worker still skips this trigger if any prior classification ran within the last `CLASSIFIER_DEDUP_SECONDS` seconds (so the initial-sync race between informer's first emit and trigger 1's immediate first run collapses to a single classification cycle).

#### **Goroutine loop**

```
for {
    select {
    case <-ctx.Done():
        return
    case <-timer.C:
        runClassification()
    case <-reclassifySignal:        // CR watcher: annotation set to "true"
        result := runClassification()
        if result == classified {
            removeReclassifyAnnotation()
        }
        // If classification skipped (e.g., insufficient history → pattern_unknown),
        // leave the annotation in place so the operator's request is not silently
        // consumed; the next trigger (timer, rollout, or fresh annotation)
        // will retry.
    case <-rolloutSignal:           // Deployment watcher: revision annotation incremented
        runClassification()
    }
}
```

Both signal channels are buffered (size 1\) with drop-and-replace semantics so a flurry of events does not queue indefinitely — the worker classifies at most once per signal source per cycle.

#### **Classification pipeline**

```
1. Query Prometheus:
       sum(rate(http_requests_total{deployment="<target>"}[2m]))[CLASSIFIER_HISTORY_HOURS * 60m:CONTEXT_DOWNSAMPLE_RESOLUTION_MIN * 60s]
   Returns up to CLASSIFIER_HISTORY_HOURS * 60 / CONTEXT_DOWNSAMPLE_RESOLUTION_MIN
   data points (default: 24 * 60 / 5 = 288). The 5-minute resolution keeps
   the cold path cheap — the classifier does not need 1-minute precision over
   24 hours; the hot path will still use 1-minute resolution over 60 minutes
   for its own forecasting.

2. If fewer than CLASSIFIER_MIN_POINTS points available:
       emit event: pattern_unknown
       return (do not write classifiedParams)

3. Compute confidence:
       high   if len(points) >= CLASSIFIER_HIGH_CONFIDENCE_POINTS
       medium if len(points) >= CLASSIFIER_MIN_POINTS

4. Extract features (see §7).

5. Classify pattern (see §7).

6. Compute recommended params from features using formulae (see §7).

6.5. Compute context block:
       full_series = the CLASSIFIER_HISTORY_HOURS series (already in memory from step 1).
       context.baselineRPS    = median(full_series)
       context.peakP95RPS     = percentile(full_series, 95)
       # IMPORTANT: trend24hSlope is rps/MINUTE (not rps/bucket). The natural
       # implementation least_squares_slope(full_series) over index positions
       # yields rps per CONTEXT_DOWNSAMPLE_RESOLUTION_MIN-minute bucket; we
       # divide by CONTEXT_DOWNSAMPLE_RESOLUTION_MIN to land in rps/min so the
       # value is directly blendable with forecast_linear_extrap's recent-
       # slope m (also rps/min). Equivalent: build x as real minutes from
       # the start of the series before fitting.
       context.trend24hSlope  = (
           least_squares_slope(full_series)
           / CONTEXT_DOWNSAMPLE_RESOLUTION_MIN
       )                                                            # rps/min, full window

       # Hourly profile: group points by UTC hour-of-day and take the median per hour.
       buckets = defaultdict(list)
       for (timestamp, value) in points:
           buckets[timestamp.utc_hour].append(value)
       hours_seen = len(buckets)
       context.hourlyProfile = [
           median(buckets[h]) if h in buckets else 0.0
           for h in range(24)
       ]
       context.hourlyProfileValid = (hours_seen >= HOURLY_PROFILE_MIN_HOURS)

7. Patch status.classifiedParams atomically with:
       pattern, scaleUpCooldownSeconds, scaleDownCooldownSeconds, maxStepSize,
       preferredForecaster, classifiedAt, historyPoints, confidence, and the
       full context block from step 6.5.

8. Emit K8s event: pattern_classified
       message:
         pattern=<name> confidence=<level> historyPoints=<n>
         recommended: scaleUpCooldown=<n> scaleDownCooldown=<n>
                      maxStep=<n> forecaster=<name>
         effective:   scaleUpCooldown=<n> scaleDownCooldown=<n>
                      maxStep=<n> forecaster=<name>
         context:     baseline=<r>rps p95=<r>rps trend24h=<r>rps/min
                      hourlyProfileValid=<bool>

       The "recommended" values are what the classifier computed (also written
       to status.classifiedParams). The "effective" values are what the
       reconciler will actually use on the next loop after applying the
       precedence chain (spec override > classified > default; see §8). When
       there are no spec overrides, recommended == effective and operators
       can ignore the second line. When a spec override is present, the
       difference is visible at a glance.
       The "context" line is informational — context is not part of the
       precedence chain (see §8).
```

No retry within a single classification cycle. If the Prometheus query fails, log and wait for the next trigger.

### **6.2 ExplainWorker**

Triggered by the reconciler after any event that changes replicas. The triggering reasoning tokens are `scale_up`, `scale_down`, `step_capped_up`, `step_capped_down`, and (when replicas also changed this reconcile) `max_replicas_binding` / `min_replicas_binding`. Cap-limited scales still change replica count and warrant explanation. `cooldown_holding_*` events do not trigger ExplainWorker because step 8's hysteresis guard suppresses any patch in that path. `max_replicas_binding` / `min_replicas_binding` events that fire **without** a replica change (i.e., the workload is already at the bound) emit the K8s Event for visibility but do not trigger ExplainWorker — prose explanation of "you're at the cap" is low-signal compared to the bare-fact event. The ExplainWorker always starts — no API key gate. If Ollama is unreachable or the model is not pulled, each failed call is logged and the controller continues normally; the next replica-changing event will trigger a fresh attempt.

#### **Channel semantics — drop-and-replace**

The channel between reconciler and worker is buffered, size 1\. The reconciler always wants the most recent scale decision to be the one explained — if a newer event arrives while a stale one is still queued, the new event replaces the stale one rather than being dropped. Concretely:

```go
// Reconciler send (never blocks).
// INVARIANT: there is exactly one sender per CR — the reconcile loop. The
// drain-then-send sequence below is safe only under this invariant; multiple
// senders could race between the drain and the send and end up with a full
// channel containing a stale event. Do not introduce a second sender.
select {
case explainCh <- req:
    // sent
default:
    // queue is full with a stale event — drain it and send the new one
    select { case <-explainCh: default: }
    select { case explainCh <- req: default: }   // best-effort; never blocks
}
```

The "stale wins, newer drops" behavior of a naive non-blocking send is the wrong default for prose explanations: operators care about the latest decision, not the first one of a burst.

#### **Goroutine loop**

```
for {
    select {
    case <-ctx.Done():
        return
    case req := <-explainCh:
        callOllamaAndEmitEvent(req)   // bounded by OLLAMA_TIMEOUT_SECONDS
    }
}
```

The worker processes one request at a time; while it is in flight, additional events arriving from the reconciler queue (or replace each other) per the drop-and-replace rule above.

#### **ExplainRequest fields**

The reconciler populates these fields from the values it has at the time of the scale event:

| Field | Source |
| ----- | ----- |
| `ReasoningToken` | `"scale_up"`, `"scale_down"`, `"step_capped_up"`, `"step_capped_down"`, `"max_replicas_binding"`, or `"min_replicas_binding"` (the last two only when the event also changed replicas; see §6.2 trigger rules) |
| `CurrentRPS` | Prometheus query result |
| `PredictedRPS` | Forecast Service response |
| `CurrentReplicas` | Deployment status |
| `RecommendedReplicas` | Post-CRD-bounds, pre-cap, pre-cooldown recommendation from step 5 (the `recommendedReplicas` local) |
| `UnboundedRecommended` | Pre-CRD-bounds raw forecaster output from step 5 (the `unboundedRecommended` local). Differs from `RecommendedReplicas` only when `max_replicas_binding` or `min_replicas_binding` fired. |
| `TargetReplicas` | Replica count being patched (post-cap; cooldown-blocked scales do not trigger ExplainWorker) |
| `MaxReplicas` | `spec.maxReplicas` (used by the prompt only for `max_replicas_binding`) |
| `MinReplicas` | `spec.minReplicas` (used by the prompt only for `min_replicas_binding`) |
| `HorizonMinutes` | Forecast Service `horizon_minutes` field |
| `ModelUsed` | Forecast Service `model_used` field |
| `Pattern` | `status.classifiedParams.pattern` (empty string if not yet classified) |
| `Confidence` | `status.classifiedParams.confidence` |
| `BaselineRPS` | `status.classifiedParams.context.baselineRPS` (zero if not classified) |
| `PeakP95RPS` | `status.classifiedParams.context.peakP95RPS` (zero if not classified) |
| `HourlyProfileValid` | `status.classifiedParams.context.hourlyProfileValid` |
| `EffectiveCooldownUp` | Resolved in reconcile preamble |
| `EffectiveCooldownDown` | Resolved in reconcile preamble |
| `EffectiveMaxStep` | Resolved in reconcile preamble |

#### **Prompt sent to Ollama**

The prompt is split into a system message (persona) and a user message (data) — the standard chat-API pattern.

System message (fixed; the same string for every request):

```
You are observing a Kubernetes autoscaler. Explain scaling decisions in 2-3
plain English sentences. Be concise, specific, and ground your explanation in
the data provided.
```

User message (templated per request):

```
Traffic pattern: {pattern} (confidence: {confidence})
Long-term context: baseline {baselineRPS:.0f} rps, p95 {peakP95RPS:.0f} rps
Current RPS: {currentRPS:.1f}, Predicted RPS ({horizonMinutes} min ahead): {predictedRPS:.1f}
Scaling: {currentReplicas} → {targetReplicas} replicas ({reasoningToken})
This scale was limited by maxStep: the controller computed {recommendedReplicas} replicas from the forecast but moved only to {targetReplicas} this reconcile (cap: {effectiveMaxStep} replicas per reconcile).
This scale was limited by maxReplicas: the forecast asked for {unboundedRecommended} replicas but the CRD bound capped it at maxReplicas={maxReplicas}. Raise spec.maxReplicas to let the autoscaler scale further.
This scale was limited by minReplicas: the forecast asked for only {unboundedRecommended} replicas but the CRD bound floored it at minReplicas={minReplicas}.
Forecasting model: {modelUsed}
Active parameters: scaleUpCooldown={effectiveCooldownUp}s, scaleDownCooldown={effectiveCooldownDown}s, maxStep={effectiveMaxStep}

Explain why this decision was made and what the traffic data suggests
relative to the workload's long-term baseline.
```

Conditional lines:

* Omit the `Traffic pattern` line when `pattern` is empty (classifier hasn't run yet) or equal to `"default"` (no dominant shape — uninformative to the model).  
* Omit the `Long-term context` line when the classifier hasn't run yet (`Pattern == ""`). Do **not** gate on `BaselineRPS == 0 && PeakP95RPS == 0`: a workload with genuinely zero recent traffic has those values legitimately measured at zero, and we want the prose to say so.  
* Include the "This scale was limited by maxStep..." line only when `ReasoningToken ∈ {step_capped_up, step_capped_down}`.  
* Include the "This scale was limited by maxReplicas..." line only when `ReasoningToken == "max_replicas_binding"`. Without this line, the LLM sees only `Scaling: 5 → 10 (max_replicas_binding)` and would generate misleading prose like "scaled up to handle predicted load" — actively hiding from the operator that the forecast asked for far more capacity than the CRD bound permits.  
* Include the "This scale was limited by minReplicas..." line only when `ReasoningToken == "min_replicas_binding"`.  
* The two `maxReplicas`/`minReplicas` lines are mutually exclusive with `step_capped_*` and with each other (the precedence chain in §5 ensures only one binding-constraint token wins per reconcile).

#### **Ollama API call**

Uses Ollama's OpenAI-compatible endpoint. No authorization header required.

```
POST {OLLAMA_URL}/v1/chat/completions
Content-Type: application/json

{
  "model": "{OLLAMA_MODEL}",
  "messages": [
    {"role": "system", "content": "<system message above>"},
    {"role": "user",   "content": "<user message above>"}
  ],
  "max_tokens": {OLLAMA_MAX_TOKENS},
  "stream": false
}
```

Timeout: `OLLAMA_TIMEOUT_SECONDS` (default: 30s).

#### **Output**

On success, emit a K8s Event:

* reason: `"ScaleExplained"` (reasoning token: `scale_explained`)  
* message: the LLM's response text, trimmed to 500 characters if longer  
* References the same `AgenticAutoscaler` object as the triggering scale event

## **7\. Feature extraction and classification rules**

### **Features**

Computed over the full available history (up to `CLASSIFIER_HISTORY_HOURS * 60 / CONTEXT_DOWNSAMPLE_RESOLUTION_MIN` points; default 288).

| Feature | Formula | What it captures |
| ----- | ----- | ----- |
| `cv` | `stddev(series) / mean(series)`; if `mean(series) < CV_GUARD_MEAN_RPS`, set `cv = 0` | Normalised spikiness — how variable traffic is relative to its average; zero-guarded against near-zero traffic. See the constants table for `CV_GUARD_MEAN_RPS`. |
| `peak_to_trough` | `percentile_99(series) / max(mean(series), 1.0)` | Burst magnitude — p99 rather than max so a single outlier minute does not dominate. The `max(mean, 1.0)` denominator prevents divide-by-zero on near-zero series **and** keeps the metric scale-aware: at high RPS the denominator is just the mean (as intended); at near-zero RPS the floor caps the result at `p99 / 1.0` rather than letting an additive `+1` regulariser dominate. The earlier draft used `mean(series) + 1` which biased low-RPS workloads downward. |
| `hourly_autocorr` | Pearson correlation of `series` with its lag-`L` shift, where `L = 60 / CONTEXT_DOWNSAMPLE_RESOLUTION_MIN` points (default `L=12`, i.e. one hour at 5-min resolution). Sets to `0` when `len(series) < L + 10` (lag plus a minimum 10-sample overlap for the correlation to be statistically meaningful). | Detects repeating hourly cycles — workloads where traffic 60 minutes ago strongly predicts traffic now. The `periodic` pattern name is a user-visible label; the underlying signal is hourly periodicity. Short-history guard prevents undefined or meaningless correlation values. |
| `trend_slope` | Identical to `context.trend24hSlope` (see §6.1 step 6.5) — the cold path computes both at the same time. Already in **rps/min** (the natural `least_squares_slope` over 5-min-resolution buckets is divided by `CONTEXT_DOWNSAMPLE_RESOLUTION_MIN` to land in rps/min, see §6.1 step 6.5 for the unit derivation). | Detects sustained upward or downward drift. Same value powers both classification (§7 rule 4) and the linear-extrap trend blend (§5 forecast_linear_extrap step 3). |

The autocorrelation gate (`L + 10`) is independent of `CLASSIFIER_MIN_POINTS`. If you tune `CONTEXT_DOWNSAMPLE_RESOLUTION_MIN` away from its default, the lag `L` changes accordingly and so does the gate; ensure `CLASSIFIER_MIN_POINTS >= L + 10` so the classifier doesn't run before autocorrelation can be computed.

### **Classification rules**

Evaluated in priority order; first match wins. The pattern name is written to `status.classifiedParams.pattern` and included in events as a human-readable label — it does not drive parameter selection. The formulae below are the mechanism.

| Priority | Pattern | Rule | Rationale |
| ----- | ----- | ----- | ----- |
| 1 | `flat` | `cv < 0.10` | Nearly constant traffic; aggressive scaling would cause churn with no benefit |
| 2 | `periodic` | `hourly_autocorr > 0.70` | Strong periodic cycle; Prophet with the hourly-baseline regressor (seeded from `context.hourly_profile`) is the right tool |
| 3 | `spiky` | `cv > 0.50 AND peak_to_trough > 5` | High variance, large bursts; needs an upper-quantile prediction, not a mean — GBDT quantile (p90) is the right tool |
| 4 | `gradual_ramp` | `abs(trend_slope) * 1440 / max(mean(series), 1) > GRADUAL_RAMP_DAILY_DRIFT_FRAC` (default 0.20 — fires when the 24h drift exceeds 20% of the workload's mean RPS). Equivalent to "the workload is rising or falling by ≥20% over the next 24h if the current slope continues." | Sustained drift; Prophet's changepoint detection beats linear extrapolation. The earlier draft used an absolute threshold (`abs(trend_slope) > 2.0 rps/min`) which fired only on workloads with 30×+ daily growth — almost never in practice. The relative threshold is scale-invariant: 20% drift triggers `gradual_ramp` whether the workload is at 50 rps or 5000 rps. |
| 5 | `default` | everything else | Moderate variance, no dominant shape |

Why `periodic` outranks `spiky` (priority 2 over 3): a workload that is *both* highly periodic (`hourly_autocorr > 0.70`) and spiky (`cv > 0.50` and `peak_to_trough > 5`) lands in `periodic` and gets `prophet`. This is intentional. Prophet with the `hour_baseline` regressor models the **shape** of the recurring cycle (when peaks happen, how high they go, how the level shifts hour-to-hour), and its `2.0×` `peak_p95_rps` safety cap absorbs the burst magnitude. GBDT-quantile is the right tool for *aperiodic* spikes (where you can only predict that a burst is coming, not when), but for periodic peaks the periodicity is the dominant signal — knowing that the next peak is at the top of the hour matters more than predicting that some peak is coming. An operator who specifically wants quantile-based capacity planning for a periodic-spiky workload can override `spec.preferredForecaster: "gbdt_quantile"` per §4.

### **Parameter formulae**

`scaleUpCooldownSeconds` — inverse of CV:

```
scaleUpCooldown = clamp(
    round(BASE_SCALEUP_COOLDOWN / (1 + K_CV_UP * cv)),
    SCALEUP_COOLDOWN_HARD_FLOOR,
    SCALEUP_COOLDOWN_HARD_CEILING
)
```

More variable traffic → shorter cooldown, faster reaction. At cv=0: 120s. At cv=0.5: 60s. At cv\>1.5: formula output falls below 30s and floor clamps to 30s.

`scaleDownCooldownSeconds` — grows with spikiness, shrinks with periodicity:

```
scaleDownCooldown = clamp(
    round(BASE_SCALEDOWN_COOLDOWN * (1 + K_CV_DOWN * cv) / (1 + K_PERIODIC_DOWN * max(0, hourly_autocorr))),
    SCALEDOWN_COOLDOWN_HARD_FLOOR,
    SCALEDOWN_COOLDOWN_HARD_CEILING
)
```

Spikier traffic → hold capacity longer (another burst may be coming). More periodic traffic → release faster between peaks (the next one is predictable). The two features compose independently.

`maxStepSize` — logarithmic on burst magnitude:

```
maxStep = clamp(ceil(log2(peak_to_trough)), 1, maxReplicas - minReplicas)
```

Burst magnitude doubling adds one to the step size. Diminishing returns is the right shape: step 1→2 matters far more than step 5→6. The upper bound is the full replica range from the CRD, so a tightly-bounded workload is automatically conservative.

`preferredForecaster` — pattern-driven:

```
if pattern == "periodic":
    preferredForecaster = "prophet"          # uses hour_baseline regressor when hourlyProfileValid
elif pattern == "spiky":
    preferredForecaster = "gbdt_quantile"
elif pattern == "gradual_ramp":
    preferredForecaster = "prophet"
else:  # flat, default
    preferredForecaster = "linear_extrap"
```

| Pattern | Forecaster | Why |
| ----- | ----- | ----- |
| `flat` | `linear_extrap` | Negligible signal; anything more is overkill |
| `periodic` | `prophet` | Prophet with the `hour_baseline` regressor (when `hourlyProfileValid` is true) captures the cold-path-observed hourly cycle correctly on a 60-min window. When `hourlyProfileValid` is false, the regressor is silently disabled and Prophet falls back on its trend/changepoint mechanism — same forecaster, no surprise swap on the operator side. |
| `spiky` | `gbdt_quantile` | p90 prediction is the right anchor for bursty workloads; mean under-provisions |
| `gradual_ramp` | `prophet` | Changepoint detection beats linear on plateauing or curving ramps |
| `default` | `linear_extrap` | No structure to exploit |

### **Constants**

| Constant | Value | Role |
| ----- | ----- | ----- |
| `BASE_SCALEUP_COOLDOWN` | 120s | Cooldown at zero variance |
| `K_CV_UP` | 2.0 | Rate at which cooldown shrinks with CV |
| `SCALEUP_COOLDOWN_HARD_FLOOR` | 30s | Reachable hard floor — formula approaches 0 as cv grows; clamp guarantees minimum reaction interval |
| `SCALEUP_COOLDOWN_HARD_CEILING` | 180s | Defensive ceiling — unreachable by the current formula (cv ≥ 0 means output ≤ BASE \= 120s); retained as a guard against future constant changes |
| `BASE_SCALEDOWN_COOLDOWN` | 180s | Cooldown at zero variance and zero periodicity |
| `K_CV_DOWN` | 1.5 | Rate at which scale-down cooldown grows with CV |
| `K_PERIODIC_DOWN` | 0.5 | Rate at which periodicity (hourly autocorrelation) reduces scale-down cooldown. _(In the source this constant is currently named `K_TOD_DOWN` — leftover from when the feature was called `tod_correlation`. Spec uses the new name; rename in code is tracked separately.)_ |
| `SCALEDOWN_COOLDOWN_HARD_FLOOR` | 60s | Defensive floor — unreachable by the current formula (minimum formula output is 120s at cv=0, hourly\_autocorr=1); retained as a guard against future constant changes |
| `SCALEDOWN_COOLDOWN_HARD_CEILING` | 600s | Reachable hard ceiling — bounds large-cv runaway (e.g., cv≈1.56 → 600s) |
| `GRADUAL_RAMP_DAILY_DRIFT_FRAC` | 0.20 | Threshold for the `gradual_ramp` classification rule — fires when projected 24h drift exceeds this fraction of mean RPS. See §7 classification rule 4 for the derivation. Hardcoded because it defines what `gradual_ramp` *means* in this autoscaler; tuning it would silently shift the meaning of the pattern label. |

All constants and classification thresholds (0.10, 0.70, 0.50, 5) live in the Controller source. They are not operator-facing knobs and do not belong in the CRD.

## **8\. Reconcile precedence**

See the §5 reconcile preamble for the four `??` (nil-coalesce) resolution rules. Each field is resolved independently, so partial overrides work — e.g., pin `maxStepSize` while leaving both cooldowns to the classifier. The final fallback tier uses the env-var defaults (`DEFAULT_SCALE_UP_COOLDOWN_SECONDS`, `DEFAULT_SCALE_DOWN_COOLDOWN_SECONDS`, `DEFAULT_MAX_STEP_SIZE`) rather than hardcoded values.

`effectiveForecaster` is passed to the Forecast Service as the optional `preferred_model` field. The literal value `"auto"` is omitted from the request, leaving the Forecast Service's auto-selection logic unchanged.

`effectiveContext` is read directly from `status.classifiedParams.context`. It is *not* part of the precedence chain — there is no `spec.context` override. Context is empirical data extracted from the cluster, not an operator preference.

The reconciler omits the `context` field in `/recommend` entirely when `status.classifiedParams.context` is absent (cold start, before the first classification). The Forecast Service treats absent context as nil and falls back to context-free forecasting behavior (no safety caps, no hourly regressor — see §5 forecaster implementations).

## **9\. Failure behavior**

| Failure | Behavior |
| ----- | ----- |
| Prophet raises (fit error, library bug, malformed series) | Fall back to `forecast_linear_extrap`; response carries `model_used: "linear_extrap"`. Increment counter `forecast_prophet_failures_total` for visibility. |
| `forecast_gbdt_quantile` raises (e.g., `sklearn` numerical issue, NaN target) | Fall back to `forecast_linear_extrap`; `model_used: "linear_extrap"` in response. Increment `forecast_gbdt_quantile_failures_total`. |
| Forecast Service unreachable / 5xx / timeout \> `FORECAST_TIMEOUT_SECONDS` | Controller logs; emits event `forecast_unavailable`; does not change replicas. Hold last-known-good state. Retry on next reconcile. |
| Prometheus query fails or returns no data (hot path) | Log; emit event `metrics_unavailable`; no-op. Retry on next reconcile. |
| `/scale` patch returns an error | Log; leave status as-is; retry on next reconcile. |
| Forecast Service returns invalid response (missing field, NaN, negative) | Log; emit event `forecast_unavailable`; no-op. |
| Forecast Service receives malformed `context` (bad types, wrong array length, etc.) | Drop `context` (treat as if absent), log warning. Proceed with context-free forecasting. Do NOT 400 — the hot path must keep working. |
| Prometheus query fails during classification (cold path) | Log; skip this cycle; retry on next trigger. No change to `status.classifiedParams`. |
| Fewer than `CLASSIFIER_MIN_POINTS` history points during classification | Emit `pattern_unknown` event; skip; retry on next trigger. |
| Fewer than `HOURLY_PROFILE_MIN_HOURS` distinct UTC hours covered during classification | `hourlyProfile` is written with zero-fill for missing hours; `hourlyProfileValid = false`. Pattern classification still proceeds. Forecast Service ignores `hourly_profile` but uses other context fields (baseline, p95 caps). |
| ClassifierWorker goroutine panics | Recovered by a `defer`/`recover` wrapper; logged; goroutine restarts after 60s backoff. |
| `preferred_model` hint causes a non-trivial model to fail | Existing fallback to `linear_extrap` applies; `model_used: "linear_extrap"` in response. |
| Reconciler reads stale `status.classifiedParams.context` (classifier hasn't run for a long time) | No special handling. Context is empirical and only goes stale if the workload changes shape; the next classifier tick (default 30 min) refreshes it. The `classifiedAt` field tells operators how fresh it is. |
| CR has no `status.classifiedParams` yet (cold start) | Reconciler falls through to env-var defaults via the nil-coalesce chain. `context` is omitted from `/recommend`. Scaling works correctly from the first reconcile; classification arrives within `CLASSIFIER_INTERVAL_MINUTES`. |
| Ollama call times out or returns 5xx | Log; no `scale_explained` event emitted; no retry. The next replica-changing event will trigger a fresh attempt. |
| Ollama model not found (model not pre-pulled) | Ollama returns 404; logged as a warning on each attempt. No `scale_explained` event emitted. Run `ollama pull {OLLAMA_MODEL}` to resolve. |
| ExplainWorker channel already has a queued event when a newer event arrives | Reconciler drains the stale event and replaces it with the new one (drop-and-replace; see §6.2). The stale event is silently discarded; the newer event is what gets explained. |
| Ollama returns empty content or malformed JSON | Log; no `scale_explained` event emitted. ExplainWorker continues waiting for the next request. |
| ExplainWorker goroutine panics | Recovered by a `defer`/`recover` wrapper; logged; goroutine restarts after 60s backoff. |

No retries within a single reconcile or classification cycle. Just wait for the next trigger. The kill-switch path (annotation set to true) is specified in §5 step 1\.
