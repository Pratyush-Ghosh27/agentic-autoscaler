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
make port-forward-grafana
# http://localhost:3000   (admin / prom-operator)
```

Navigate to *Dashboards → Browse → "Agentic Autoscaler"*.

## Manual import (sidecar fallback)

If the sidecar isn't running (custom Helm values, non-standard install,
etc.) you can import the JSON directly:

```sh
make port-forward-grafana &
sleep 2

curl -s -X POST http://admin:prom-operator@localhost:3000/api/dashboards/db \
  -H "Content-Type: application/json" \
  -d "$(jq '{dashboard: ., overwrite: true}' deploy/grafana/agentic-dashboard.json)"
```

## Panels

| #  | Panel                  | Source metric                                           | Notes                                  |
| -- | ---------------------- | ------------------------------------------------------- | -------------------------------------- |
| 1  | Current RPS            | `target_app_requests_total` rate over 2m                | Both targets overlaid                  |
| 2  | Replica count          | `kube_deployment_spec_replicas`                         | The headline scaling comparison        |
| 3  | Predicted RPS          | controller annotation/Event-derived series              | Latest forecast for the agentic target |
| 4  | p99 latency            | `target_app_request_duration_seconds_bucket` histogram  | The SLO-relevant tail metric           |
| 5  | 5xx rate               | `target_app_requests_total{status=~"5.."}` rate         | Lower-is-better for both reconcilers   |
| 6  | Scaling Events         | `kube_event_count`                                       | Filtered by reasoning tokens           |
| 7  | Classified pattern     | controller status / kube-state-metrics CR projection    | Current pattern + confidence           |

## Refreshing after a metric rename

If the controller or target-app changes a metric name, regenerate the
ConfigMap by re-running `make deploy` — Kustomize's
`configMapGenerator` produces a new content-hash-suffixed name and the
sidecar picks it up on the next sync (~30 s).

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
