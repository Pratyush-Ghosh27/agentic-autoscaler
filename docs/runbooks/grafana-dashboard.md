# Grafana Dashboard Runbook

The Agentic Autoscaler ships with a 7-panel Grafana dashboard
(`deploy/grafana/agentic-dashboard.json`) that side-by-side compares the
agentic-managed target with the HPA-managed target, plus the controller
internals (predicted RPS, classified pattern).

## Auto-import (recommended)

`deploy/grafana/kustomization.yaml` uses `configMapGenerator` to embed
the JSON into a ConfigMap labeled `grafana_dashboard: "1"`. The
kube-prometheus-stack Grafana sidecar watches for ConfigMaps with this
label and auto-imports them.

After `make deploy`:

```sh
make grafana-url
# http://localhost:30080   (admin / admin)
```

The Grafana Service is published as a NodePort on the kind control-plane
node (pinned to 30080 in `deploy/helm/prometheus-values.yaml`), and
`deploy/kind/cluster.yaml`'s `extraPortMappings` forwards the container's
port 30080 to the host. The URL is permanent for the lifetime of the
cluster, survives SSH disconnects to the host VM, and is reachable from
elsewhere on the network at `http://<vm-ip>:30080`. (`make
port-forward-grafana` is still wired as a fallback that proxies to
`localhost:3000` if the NodePort path isn't viable in your environment.)

Navigate to *Dashboards → Browse → "Agentic Autoscaler"*.

## Manual import (sidecar fallback)

If the sidecar isn't running (custom Helm values, non-standard install,
etc.) you can import the JSON directly:

```sh
curl -s -X POST http://admin:admin@localhost:30080/api/dashboards/db \
  -H "Content-Type: application/json" \
  -d "$(jq '{dashboard: ., overwrite: true}' deploy/grafana/agentic-dashboard.json)"
```

## Panels

| #  | Panel                  | Source metric                                           | Notes                                  |
| -- | ---------------------- | ------------------------------------------------------- | -------------------------------------- |
| 1  | Current RPS            | `http_requests_total` rate over 2m                | Both targets overlaid                  |
| 2  | Replica count          | `kube_deployment_spec_replicas`                         | The headline scaling comparison        |
| 3  | Predicted RPS          | controller annotation/Event-derived series              | Latest forecast for the agentic target |
| 4  | p99 latency            | `http_request_duration_seconds_bucket` histogram  | The SLO-relevant tail metric           |
| 5  | 5xx rate               | `http_requests_total{status=~"5.."}` rate         | Lower-is-better for both reconcilers   |
| 6  | Scaling Events         | `kube_event_count`                                       | Filtered by reasoning tokens           |
| 7  | Classified pattern     | controller status / kube-state-metrics CR projection    | Current pattern + confidence           |

## Refreshing after a metric rename

If the controller or target-app changes a metric name, regenerate the
ConfigMap by re-running `make deploy` — Kustomize's
`configMapGenerator` produces a new content-hash-suffixed name and the
sidecar picks it up on the next sync (~30 s).

## v2 changes that affect custom dashboards

If you maintain custom Grafana panels alongside this one, two v2
changes will silently break filters:

- **K8s Event `Reason` field is PascalCase.** Plan 16 / G22 / F39
  migrated the K8s Event `Reason` field from snake_case (`scale_up`,
  `step_capped_up`, `max_replicas_binding`) to PascalCase (`ScaleUp`,
  `StepCappedUp`, `MaxReplicasBinding`) per K8s convention. Custom
  panels filtering on `kube_event_count{reason="scale_up"}` will return
  empty after upgrade — update them to `reason="ScaleUp"`. The
  shipped *Scaling Events* panel (#6) was updated in Plan 17 / cosmetic
  follow-up. The full snake_case ↔ PascalCase mapping lives in
  [`internal/reasoning/tokens.go`](../../internal/reasoning/tokens.go)
  (`PascalReason`).

- **New Forecast Service metric: `forecast_dispatch_total{model_used}`.**
  Plan 18 added a counter labelled by the resolved (post-fallback)
  forecaster. Useful as a stacked-bar panel showing
  `sum by (model_used) (rate(forecast_dispatch_total[5m]))` to make
  forecaster traffic share visible at a glance. The nightly E2E asserts
  on `model_used="gbdt_quantile" > 0` after a `preferredForecaster`
  patch (see `docs/runbooks/nightly-e2e.md`).

## Troubleshooting

- **Dashboard not appearing** — verify the ConfigMap exists and is
  labeled correctly:

  ```sh
  kubectl get cm -n monitoring -l grafana_dashboard=1
  ```

  If it's missing, `make deploy` again. If it exists but Grafana doesn't
  show it, check the sidecar logs:

  ```sh
  kubectl logs -n monitoring -l app.kubernetes.io/name=grafana -c grafana-sc-dashboard --tail=50
  ```

- **Empty panels** — the metrics expect Prometheus to be scraping the
  target-app `/metrics` endpoint. Confirm with:

  ```sh
  kubectl port-forward -n monitoring svc/kube-prom-kube-prometheus-prometheus 9090:9090
  # http://localhost:9090/targets — both target-app pods should be UP
  ```

- **Wrong p99 values** — the histogram's bucket boundaries must match
  the target-app's `Buckets` slice. If you customized the bucket list,
  re-export the dashboard panel from Grafana → Edit → JSON.

- **Dashboard shows only one target** — verify both `app-agentic` and
  `app-hpa` are emitting metrics with the `deployment` label. The
  PodMonitor / ServiceMonitor sets this label from the parent Deployment.
