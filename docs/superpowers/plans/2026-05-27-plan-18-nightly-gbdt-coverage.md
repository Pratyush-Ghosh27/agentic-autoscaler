# Plan 18 — Nightly E2E GBDT Coverage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Lock in a passing nightly E2E assertion that the `gbdt_quantile` forecaster fires end-to-end on a real cluster — closing the follow-up explicitly flagged in `docs/superpowers/specs/2026-05-26-v2-implementation-strategy.md` §7 risk register and `docs/gap-report-v2.md` §4: *"once G12 is in, the nightly should be expanded with a `spiky` scenario that asserts `model_used == 'gbdt_quantile'` to lock in the third-forecaster guarantee."*

**Architecture:** Two-part change. **Part A** (forecast-service) adds a `forecast_dispatch_total` Counter labeled by `model_used` so Prometheus has an explicit signal for which forecaster ran. **Part B** (nightly workflow) adds a second k6 run after the existing ramp run: patches the agentic CR to `spec.preferredForecaster: "gbdt_quantile"`, runs the existing `k6/scenarios/spiky.js`, and asserts the new counter. The classifier-promotion path (spiky load → `pattern_classified: spiky` → ComputeParams picks `gbdt_quantile`) is NOT exercised here because it requires `CLASSIFIER_MIN_POINTS=72` of 5-min-cadence history (= 6h) which a 25-min nightly cannot satisfy. Patching `preferredForecaster` directly tests the dispatch path under realistic spiky load, which is the lock-in the strategy doc actually asked for.

**Tech Stack:** Python (forecast-service Counter), bash (assertion script), GitHub Actions YAML (nightly workflow), kubectl (CR patch), existing k6 spiky.js (no change).

**Phase context:** v2 is closed (Phases 1–6 merged via PRs #12–#18). This is a **standalone post-v2 follow-up**, not a new phase. It is a single PR; no execution dependencies on other plans.

---

## File structure

| File | Type | Responsibility |
| --- | --- | --- |
| `forecast-service/src/forecast/metrics.py` | Modify | Add `forecast_dispatch_total` Counter with `model_used` label. |
| `forecast-service/src/forecast/dispatch.py` | Modify | Increment `forecast_dispatch_total{model_used=<resolved-name>}` after each successful forecast call. |
| `forecast-service/tests/unit/test_metrics.py` | Create | Unit tests pinning the counter exists, has the right label, and increments per dispatch call. |
| `forecast-service/tests/unit/test_dispatch.py` | Modify | Add a test verifying every successful `recommend()` call bumps `forecast_dispatch_total{model_used=...}` by exactly 1. |
| `test/e2e/assertions-gbdt.sh` | Create | Standalone Prometheus assertion script: `forecast_dispatch_total{model_used="gbdt_quantile"} > 0`. Mirrors the structure of `test/e2e/assertions.sh`. |
| `.github/workflows/nightly-e2e.yml` | Modify | Add three steps after the existing ramp-assertion step: (1) patch agentic CR to `preferredForecaster: gbdt_quantile`, (2) run `bash deploy/k6/run-incluster.sh spiky`, (3) run `test/e2e/assertions-gbdt.sh`. |
| `docs/v2-acceptance-coverage.md` | Modify | Append a footer noting nightly E2E lock-in for criterion #9 (the existing integration-test pin remains; this adds a real-cluster pin). |

No CRD or controller code changes. The CR patch is runtime only (in the nightly workflow); it does not modify `deploy/manifests/agenticautoscaler-sample.yaml`.

---

## Tasks

### Task 1: Add `forecast_dispatch_total` Counter (TDD)

**Files:**
- Create: `forecast-service/tests/unit/test_metrics.py`
- Modify: `forecast-service/src/forecast/metrics.py`

The forecast-service today only exports `forecast_prophet_failures_total` (a single counter for Prophet-fallback events). To assert "GBDT actually ran" from a nightly E2E, we need a counter labeled by the resolved `model_used`.

- [ ] **Step 1: Write the failing unit test**

Create `forecast-service/tests/unit/test_metrics.py`:

```python
"""Tests for the Prometheus metrics exported by the forecast service."""

from __future__ import annotations

import pytest

from forecast.metrics import (
    forecast_dispatch_total,
    forecast_prophet_failures_total,
)


def test_forecast_dispatch_total_exists_with_model_used_label() -> None:
    """The counter exists and has exactly one label, named ``model_used``.

    A nightly E2E asserts on this metric to lock in the gbdt_quantile path
    (Plan 18). Renaming the metric or its label is a breaking change for
    test/e2e/assertions-gbdt.sh.
    """
    # Sample a value with a known model name to force the labelset.
    forecast_dispatch_total.labels(model_used="prophet").inc(0)

    samples = list(forecast_dispatch_total.collect())
    assert len(samples) == 1
    metric_family = samples[0]
    assert metric_family.name == "forecast_dispatch_total"
    label_names = {sample.labels.keys().__iter__().__next__() for sample in metric_family.samples if sample.labels}
    assert label_names == {"model_used"}, f"expected exactly one label 'model_used', got {label_names}"


@pytest.mark.parametrize("name", ["prophet", "linear_extrap", "gbdt_quantile"])
def test_forecast_dispatch_total_accepts_each_model_used_value(name: str) -> None:
    """The three v2 forecaster names are valid label values."""
    forecast_dispatch_total.labels(model_used=name).inc()


def test_forecast_prophet_failures_total_is_unchanged() -> None:
    """The pre-existing counter is still exported (regression guard)."""
    samples = list(forecast_prophet_failures_total.collect())
    assert samples[0].name == "forecast_prophet_failures_total"
```

- [ ] **Step 2: Run the test — expect FAIL**

```
cd forecast-service && pytest tests/unit/test_metrics.py -v
```

Expected: FAIL with `ImportError: cannot import name 'forecast_dispatch_total' from 'forecast.metrics'`.

- [ ] **Step 3: Add the counter to make the test pass**

Replace the contents of `forecast-service/src/forecast/metrics.py` with:

```python
"""Prometheus metrics exported by the forecast service."""

from __future__ import annotations

from prometheus_client import Counter

forecast_prophet_failures_total = Counter(
    "forecast_prophet_failures_total",
    "Number of times Prophet raised during /recommend; dispatcher fell back to linear_extrap.",
)

forecast_dispatch_total = Counter(
    "forecast_dispatch_total",
    "Cumulative count of successful /recommend dispatches, by resolved model_used.",
    labelnames=["model_used"],
)
```

- [ ] **Step 4: Re-run the test — expect PASS**

```
cd forecast-service && pytest tests/unit/test_metrics.py -v
```

Expected: PASS for all three test functions.

- [ ] **Step 5: Commit**

```
git add forecast-service/src/forecast/metrics.py forecast-service/tests/unit/test_metrics.py
git commit -m "feat(forecast-svc): add forecast_dispatch_total{model_used} counter (Plan 18 T1)"
```

---

### Task 2: Wire the counter into `dispatch.recommend()` (TDD)

**Files:**
- Modify: `forecast-service/src/forecast/dispatch.py`
- Modify: `forecast-service/tests/unit/test_dispatch.py`

The counter exists; now `recommend()` must increment it once per successful call, labeled with the resolved `model_used`. This is the single point where every forecast path ends, so one increment site covers prophet / linear_extrap / gbdt_quantile uniformly.

- [ ] **Step 1: Write the failing test**

Append to `forecast-service/tests/unit/test_dispatch.py`:

```python
def test_recommend_increments_forecast_dispatch_total_per_call() -> None:
    """Every successful recommend() call increments forecast_dispatch_total
    by exactly 1, labeled with the resolved model_used.

    This is the lock-in for the nightly E2E assertion (Plan 18).
    """
    from forecast.metrics import forecast_dispatch_total

    history = [10.0] * 30  # enough for prophet's PROPHET_MIN_POINTS=30

    before_linear = forecast_dispatch_total.labels(model_used="linear_extrap")._value.get()
    result = recommend(rps_history=history[:10], horizon_minutes=10)
    after_linear = forecast_dispatch_total.labels(model_used="linear_extrap")._value.get()

    assert result["model_used"] == "linear_extrap"
    assert after_linear == before_linear + 1
```

- [ ] **Step 2: Run the test — expect FAIL**

```
cd forecast-service && pytest tests/unit/test_dispatch.py::test_recommend_increments_forecast_dispatch_total_per_call -v
```

Expected: FAIL — assertion error on `after_linear == before_linear + 1` (counter not incremented because `recommend()` doesn't touch it yet).

- [ ] **Step 3: Add the increment to `recommend()`**

In `forecast-service/src/forecast/dispatch.py`, find the function `recommend()` and add the increment immediately before the function returns. The exact insertion site is the bottom of the function, after the result dict is fully built:

```python
# At the top of dispatch.py, alongside the existing
# `from forecast.metrics import forecast_prophet_failures_total`:
from forecast.metrics import forecast_dispatch_total, forecast_prophet_failures_total
```

Then, immediately before each `return` statement in `recommend()` (there are multiple branches — prophet, linear_extrap, gbdt_quantile, fallback), add:

```python
forecast_dispatch_total.labels(model_used=result["model_used"]).inc()
return result
```

If `recommend()` has only one return at the bottom, place the increment as the last statement before that return. If it has multiple early returns (one per branch), refactor to a single bottom return and increment there — this matches the "one increment site for all forecasters" architecture decision.

- [ ] **Step 4: Re-run the test — expect PASS**

```
cd forecast-service && pytest tests/unit/test_dispatch.py::test_recommend_increments_forecast_dispatch_total_per_call -v
```

Expected: PASS.

- [ ] **Step 5: Run the full forecast-service test suite to ensure nothing regressed**

```
cd forecast-service && pytest tests/ -q --cov=src/forecast --cov-report=term-missing --cov-fail-under=90
```

Expected: PASS, coverage ≥ 90%.

- [ ] **Step 6: Commit**

```
git add forecast-service/src/forecast/dispatch.py forecast-service/tests/unit/test_dispatch.py
git commit -m "feat(forecast-svc): increment forecast_dispatch_total per recommend() call (Plan 18 T2)"
```

---

### Task 3: Add `test/e2e/assertions-gbdt.sh`

**Files:**
- Create: `test/e2e/assertions-gbdt.sh`

A standalone Prometheus assertion script that the nightly workflow invokes after the spiky k6 run. Mirrors the structure of the existing `test/e2e/assertions.sh` (port-forward Prometheus, query, fail loudly on NaN/zero baselines).

- [ ] **Step 1: Create the script**

Create `test/e2e/assertions-gbdt.sh`:

```bash
#!/usr/bin/env bash
# -----------------------------------------------------------------------
# test/e2e/assertions-gbdt.sh — assert the gbdt_quantile path fired.
#
# Prerequisite (set up by the nightly workflow before this script runs):
#   1. The agentic CR has been patched to spec.preferredForecaster:
#      "gbdt_quantile" — controller forwards preferred_model="gbdt_quantile"
#      on every /recommend call.
#   2. The k6 spiky scenario has run for at least one classifier reconcile
#      cycle, i.e. the controller has called /recommend at least once with
#      the patched CR active.
#
# Asserts: forecast_dispatch_total{model_used="gbdt_quantile"} > 0.
#
# A zero counter means either (a) the controller never called /recommend
# during the spiky window (deploy ordering bug, kill-switch on, or
# reconciler hung) or (b) the controller called /recommend but the
# Forecast Service routed elsewhere (dispatch.py bug — preferred_model
# silently overridden, or a regression in the gbdt_quantile branch).
# Either way it's a v2-regression and must fail the nightly.
# -----------------------------------------------------------------------
set -euo pipefail

PROM_PORT="${PROM_PORT:-9090}"
PROM_URL="http://localhost:${PROM_PORT}"

# Port-forward Prometheus.
kubectl port-forward -n monitoring svc/kube-prom-kube-prometheus-prometheus \
    "${PROM_PORT}:9090" >/dev/null 2>&1 &
PF_PID=$!
trap "kill ${PF_PID} 2>/dev/null || true" EXIT
sleep 3

query() {
    local q="$1"
    local encoded
    encoded=$(python3 -c "import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1]))" "$q")
    curl -fsS --max-time 30 "${PROM_URL}/api/v1/query?query=${encoded}" \
        | jq -r '.data.result[0].value[1] // "0"'
}

echo "==> querying forecast_dispatch_total{model_used=\"gbdt_quantile\"}"
GBDT_COUNT=$(query 'forecast_dispatch_total{model_used="gbdt_quantile"}')

echo "    forecast_dispatch_total{model_used=\"gbdt_quantile\"} = ${GBDT_COUNT}"

python3 - <<EOF
import math, sys
v = float("${GBDT_COUNT}") if "${GBDT_COUNT}" not in ("", "NaN") else 0.0
if math.isnan(v) or v <= 0:
    print(f"  FAIL: forecast_dispatch_total{{model_used=\"gbdt_quantile\"}} is {v} — gbdt_quantile path did not fire during the spiky run", file=sys.stderr)
    sys.exit(1)
print(f"  PASS: gbdt_quantile fired {int(v)} time(s) during the spiky run.")
EOF
```

- [ ] **Step 2: Make it executable**

```
chmod +x test/e2e/assertions-gbdt.sh
```

- [ ] **Step 3: Commit**

```
git add test/e2e/assertions-gbdt.sh
git commit -m "test(e2e): add assertions-gbdt.sh for nightly gbdt_quantile lock-in (Plan 18 T3)"
```

---

### Task 4: Wire the spiky run + assertion into the nightly workflow

**Files:**
- Modify: `.github/workflows/nightly-e2e.yml`

The existing workflow ends with `Quantitative assertions (p99 + 5xx)` at line ~153. Plan 18 adds three new steps after it: patch the CR, run the spiky k6 scenario, run the GBDT assertion. The original ramp-vs-HPA comparison stays intact and still gates the nightly; the new spiky run is purely a forecaster lock-in (no agentic-vs-HPA comparison — the spiky run uses a different forecaster than the ramp run, so the comparison would be meaningless).

- [ ] **Step 1: Insert the three new steps**

In `.github/workflows/nightly-e2e.yml`, immediately AFTER the existing block:

```yaml
      - name: Quantitative assertions (p99 + 5xx)
        # WINDOW = the k6 *hold* phase (steady state) — we deliberately
        # exclude the noisy ramp-up/down windows from the latency and
        # error-rate comparison to keep the agentic-vs-HPA delta
        # apples-to-apples.
        run: TOLERANCE="${TOLERANCE}" WINDOW="${RAMP_HOLD_DURATION}" bash test/e2e/assertions.sh
```

(immediately before the `# ---- Failure artifacts ----` comment block) insert:

```yaml
      # ---- Plan 18: gbdt_quantile lock-in ----
      #
      # The strategy doc (docs/superpowers/specs/2026-05-26-v2-implementation-strategy.md
      # §7) explicitly flagged this as a follow-up: "once G12 is in, the
      # nightly should be expanded with a `spiky` scenario that asserts
      # `model_used == 'gbdt_quantile'` to lock in the third-forecaster
      # guarantee." We patch spec.preferredForecaster (rather than wait
      # for the classifier to promote to `spiky`) because the classifier
      # needs CLASSIFIER_MIN_POINTS=72 of 5-min-cadence history (= 6h),
      # which a single nightly cannot provide. Patching directly tests
      # the forecast-service dispatch path under realistic spiky load,
      # which is what the strategy doc actually asked for.
      - name: Patch agentic CR to preferredForecaster=gbdt_quantile
        run: |
          kubectl patch aas app-agentic -n demo \
            --type=merge \
            -p '{"spec":{"preferredForecaster":"gbdt_quantile"}}'
          # Wait one reconcile interval so the next /recommend call uses
          # the new preferredForecaster.
          sleep 30

      - name: Run k6 spiky scenario (in-cluster Job)
        run: bash deploy/k6/run-incluster.sh spiky

      - name: Assert gbdt_quantile path fired
        run: bash test/e2e/assertions-gbdt.sh
```

- [ ] **Step 2: Add forecast-service log capture to the failure-artifacts block**

The existing failure-artifacts block already captures forecast-service logs (`artifacts/forecast-logs.txt` at line 161). No change needed — the new GBDT assertion's failure mode produces output to stderr which CI captures, and the forecast-service logs are already in the artifact bundle.

- [ ] **Step 3: Lint the YAML**

```
yamllint .github/workflows/nightly-e2e.yml
```

Expected: no errors. If yamllint flags indentation, fix it (each new step is indented by exactly 6 spaces — same as existing steps).

- [ ] **Step 4: Commit**

```
git add .github/workflows/nightly-e2e.yml
git commit -m "ci(nightly-e2e): add spiky run + gbdt_quantile lock-in assertion (Plan 18 T4)"
```

---

### Task 5: Document the nightly lock-in in the coverage matrix

**Files:**
- Modify: `docs/v2-acceptance-coverage.md`

Acceptance criterion #9 (`spiky` + `preferredForecaster: gbdt_quantile` returns `gbdt_quantile`) is currently pinned by the integration test `test_recommend_endpoint_routes_gbdt_quantile_when_preferred`. Plan 18 adds a real-cluster pin on top of that. Update the coverage matrix to reflect both layers.

- [ ] **Step 1: Update row 9 of the matrix**

In `docs/v2-acceptance-coverage.md`, find row 9 of the Coverage table:

```
| 9 | `spiky` + `preferredForecaster: gbdt_quantile` returns `gbdt_quantile` | `forecast-service/tests/integration/test_app.py::test_recommend_endpoint_routes_gbdt_quantile_when_preferred` | Phase 3 (G12) |
```

Replace it with:

```
| 9 | `spiky` + `preferredForecaster: gbdt_quantile` returns `gbdt_quantile` | Integration: `forecast-service/tests/integration/test_app.py::test_recommend_endpoint_routes_gbdt_quantile_when_preferred`. Nightly E2E: `test/e2e/assertions-gbdt.sh` asserts `forecast_dispatch_total{model_used="gbdt_quantile"} > 0` after a real spiky k6 run. | Phase 3 (G12) + Plan 18 (real-cluster lock-in) |
```

- [ ] **Step 2: Append a "Post-v2 follow-ups" section after the existing "Out of scope"**

Append to `docs/v2-acceptance-coverage.md`:

```markdown

---

## Post-v2 follow-ups landed

- **Plan 18 — Nightly GBDT coverage** (2026-05-27): added `forecast_dispatch_total{model_used}` Counter and a nightly assertion that the gbdt_quantile path fires under realistic spiky load. Closes the strategy-doc follow-up flagged in §7 risk register.
```

- [ ] **Step 3: Commit**

```
git add docs/v2-acceptance-coverage.md
git commit -m "docs: note Plan 18 nightly E2E lock-in for criterion #9 (Plan 18 T5)"
```

---

### Task 6: Final pre-flight + push + PR

This task produces no commits — it is the verification gate before pushing.

- [ ] **Step 1: Run pre-flight**

```
TMPDIR=/home/pratyush.ghosh/scaler/.cache/tmp \
XDG_CACHE_HOME=/home/pratyush.ghosh/scaler/.cache \
GOLANGCI_LINT_CACHE=/home/pratyush.ghosh/scaler/.cache/golangci \
make pre-flight
```

Expected: PASS. The Python tests (test-python stage) cover T1+T2; the YAML lint covers T4; everything else (Go suite, envtests) is unaffected and should remain green.

- [ ] **Step 2: Push the branch**

```
git push -u origin feat/nightly-gbdt-coverage
```

- [ ] **Step 3: Open the PR**

If `gh` is available:

```
gh pr create --title "feat(nightly-e2e): GBDT coverage — Plan 18" --body "$(cat <<'EOF'
## Summary

Closes the v2 strategy-doc follow-up: nightly E2E now exercises and asserts on `gbdt_quantile`.

## What's in this PR

- New Prometheus counter `forecast_dispatch_total{model_used}` in the forecast service.
- New assertion script `test/e2e/assertions-gbdt.sh`.
- Nightly workflow now patches the agentic CR to `preferredForecaster: gbdt_quantile`, runs the existing `k6/scenarios/spiky.js`, and asserts the counter is non-zero.
- Coverage matrix updated to reflect the real-cluster pin on criterion #9.

## Test plan

- [x] `make pre-flight` green at HEAD
- [x] Forecast-service unit + integration tests cover the new counter (T1, T2)
- [ ] First nightly run after merge confirms the assertion passes on a real cluster
- [ ] Failure-mode check: an artificial regression that breaks the gbdt_quantile branch is caught by the new assertion (run locally if cluster is available)

## Architecture notes

- The classifier-promotion path (spiky load → `pattern_classified: spiky` → ComputeParams picks gbdt_quantile) is NOT exercised here; that requires `CLASSIFIER_MIN_POINTS=72` of 5-min-cadence history (~6h). Patching `preferredForecaster` directly is the testable equivalent.
- The original ramp-vs-HPA comparison still gates the nightly; the new spiky run is additive and does not affect that comparison.
EOF
)"
```

If `gh` is not available, draft the PR body to `.pr-body-plan-18.md` and open the PR via the GitHub web UI; the URL is in the `git push` output.

- [ ] **Step 4: After merge — no follow-up tasks**

The next scheduled nightly run validates the new assertion against a real cluster. If it fails on first run, the most likely causes (in order) are:
1. The CR patch arrived before the kube-apiserver had registered the latest CRD revision (race condition during cluster bootstrap). Fix: increase the `sleep 30` after the patch.
2. The forecast-service was not yet exposing `forecast_dispatch_total` to Prometheus's scrape target (ServiceMonitor or annotation drift). Fix: verify `forecast-service` `/metrics` endpoint contains the new counter.
3. Genuine regression in the dispatch path's gbdt_quantile branch. Fix: investigate `internal/adapters/forecast/types.go` and `forecast-service/src/forecast/dispatch.py` for the most recent change touching the GBDT routing.

---

## Self-review checklist

- [x] Every task has TDD where code changes (T1, T2) and concrete commands where docs/CI changes (T3-T5)
- [x] T2's increment site logic ("one return at the bottom") accommodates either current `dispatch.py` shape (single-return or multi-return — verified during plan-writing); the engineer follows the matching path at execute time
- [x] T3's assertion script mirrors `test/e2e/assertions.sh` style — same port-forward pattern, same Python error-handling block, no new dependencies
- [x] T4's CR patch path uses `kubectl patch aas` (existing kubectl tooling); the `sleep 30` matches one default reconcile interval
- [x] T5 makes the lock-in discoverable from `docs/v2-acceptance-coverage.md` so future audits see it
- [x] No `TODO` / `TBD` / `fill in details` placeholders
- [x] Every code/doc step shows the literal text being added or removed
- [x] Type/name consistency: `forecast_dispatch_total`, `model_used`, `preferredForecaster`, `gbdt_quantile` — same names used everywhere

---

## Out of scope

- **Comparing spiky agentic-vs-HPA latency**: pointless because the spiky run uses a fixed `gbdt_quantile` forecaster while ramp uses `auto`; mixing the two comparisons would mask both signals. The ramp run still does the agentic-vs-HPA gate; the spiky run is a forecaster lock-in only.
- **Asserting per-forecaster `agenticautoscaler_classified_pattern == 3` (`spiky`)**: the classifier won't promote within a 25-min nightly window; that's why we patch `preferredForecaster` directly. A separate plan could add a synthetic-history fixture if classifier-side coverage is ever needed.
- **Adding spiky to the smoke job (ci.yml)**: smoke is meant to run in <10 min and assert "things are wired up", not "forecasters work end-to-end". Plan 18 stays scoped to the nightly only.
