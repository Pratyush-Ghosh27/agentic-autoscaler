# target-app

Instrumented HTTP server used by the agentic autoscaler's controlled
experiment. Deployed twice as `app-agentic` (managed by the operator)
and `app-hpa` (managed by a standard K8s HPA) under identical traffic,
so the two scalers can be compared on identical workloads.

## Endpoints

| Path | Behaviour |
|---|---|
| `GET /work` | Acquires a semaphore slot, sleeps `TARGET_WORK_DURATION_MS ± TARGET_WORK_JITTER_MS`, returns 200. Returns 503 immediately if no slot is available. |
| `GET /healthz` | Always returns 200 `{"status":"ok"}`. |
| `GET /readyz` | Returns 200 by default; 503 if `SetReady(false)` has been called (test hook). |
| `GET /metrics` | Prometheus exposition: `http_request_duration_seconds` (histogram, 1 ms-10 s buckets, label: `path`) and `http_requests_total` (counter, labels: `path`, `status`). |

## Configuration

| Env var | Default | Notes |
|---|---|---|
| `TARGET_PORT` | `8080` | Port to listen on. |
| `TARGET_CONCURRENCY` | `8` | Semaphore size. Concurrent /work requests above this return 503. |
| `TARGET_WORK_DURATION_MS` | `50` | Base simulated work duration per /work call. |
| `TARGET_WORK_JITTER_MS` | `30` | Random jitter added on top of base duration. |

## Build

```bash
cd target-app
go build ./...
go test ./...
docker build -t agentic-target-app:dev .
```
