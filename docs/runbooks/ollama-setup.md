# Ollama Setup Runbook

The ExplainWorker ([`design_v2.md`](../design_v2.md) §6.2) calls Ollama via the OpenAI-compatible
`/v1/chat/completions` endpoint to generate plain-English scaling
explanations. Ollama is **optional** — without it, the controller still
scales correctly; only the `ScaleExplained` Events go silent.

> **v2 note:** the prompt template was extended in v2 / Plan 15 / F33 with conditional lines for `max_replicas_binding` / `min_replicas_binding` reasoning tokens (so the LLM sees `unboundedRecommended` and the binding CRD bound directly, instead of generating misleading "scaled up to handle load" prose at the cap). Without this, the worker would actively hide capacity-planning signals from the operator. If you're running a custom prompt template, mirror the conditionals in [`internal/explainer/prompt.go`](../../internal/explainer/prompt.go).

## Install

```sh
curl -fsSL https://ollama.com/install.sh | sh
```

Start the server (binds to `127.0.0.1:11434` by default):

```sh
ollama serve
```

The controller's `OLLAMA_URL` defaults to `http://localhost:11434`. When
running on kind, expose host Ollama through the kind network or run
Ollama as a Pod (out of scope for this runbook).

## Pull a model

| Environment | Model      | Why                                        |
| ----------- | ---------- | ------------------------------------------ |
| Local dev   | `llama3.2` | Better explanations; ~2 GB on disk         |
| CI nightly  | `phi3`     | Smaller (~2.3 GB) and faster on shared CPU |

```sh
make ollama-pull       # llama3.2 (local default)
make ollama-pull-ci    # phi3 (CI default)
```

## Verify

```sh
curl http://localhost:11434/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "llama3.2",
    "messages": [{"role": "user", "content": "ping"}],
    "max_tokens": 8,
    "stream": false
  }'
```

Expected response: a JSON body with a non-empty
`choices[0].message.content`.

## Wire into the controller

In-cluster, set the env vars on the controller Deployment (already
configured by the kustomize bundle in `config/default`):

```yaml
- name: OLLAMA_URL
  value: http://ollama.ollama.svc:11434
- name: OLLAMA_MODEL
  value: llama3.2
- name: OLLAMA_TIMEOUT_SECONDS
  value: "30"
- name: OLLAMA_MAX_TOKENS
  value: "150"
```

Locally:

```sh
OLLAMA_URL=http://localhost:11434 \
OLLAMA_MODEL=llama3.2 \
make run
```

## Troubleshooting

- **`404 model not found`** — run `ollama pull <model>` again. The
  ExplainWorker logs `ollama model not found; run \`ollama pull <model>\``
  at INFO level (not ERROR — it's a benign config drift).
- **`connection refused`** — `ollama serve` isn't running, or the kind
  pod can't reach the host on `localhost`. On kind, prefer running
  Ollama as a Pod or use `host.docker.internal` from the controller.
- **Slow first response** — first call after model load takes 5–10 s for
  weight loading. Subsequent calls are sub-second on warm models.
- **`ScaleExplained` Events missing** — check controller logs for
  `ollama call failed`; per design §9 the worker logs and continues, so
  the absence of Events is expected when Ollama is misconfigured. The
  rest of the reconcile loop is unaffected.

## Disabling explanations

Set `OLLAMA_URL=` to anything unreachable; the worker will log per
request and emit no Events. If you also want to suppress the log noise,
deploy without Ollama at all — every error path is silenced after a few
log lines and the goroutine continues idle until the next ExplainRequest.
