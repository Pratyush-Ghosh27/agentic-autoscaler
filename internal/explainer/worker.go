/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package explainer

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	autoscalingv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/api/v1alpha1"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/ollama"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/controller"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/reasoning"
)

// PanicBackoff matches the classifier worker — see comment there. Var so
// tests can shrink it.
var PanicBackoff = 60 * time.Second

// WorkerConfig is the env-var-driven slice the worker depends on.
// Exposed as a struct so main.go builds it once and passes it in.
type WorkerConfig struct {
	Model     string
	MaxTokens int
	Timeout   time.Duration
}

// OllamaChatter is the subset of *ollama.Client the worker uses.
// Defined here (not in the ollama package) so tests can substitute fakes.
type OllamaChatter interface {
	Chat(ctx context.Context, req ollama.ChatRequest) (string, error)
}

// Worker is the ExplainWorker goroutine: one per controller process,
// shared across all CRs. Reads ExplainRequest values from the channel
// produced by the reconciler's ChannelNotifier, calls Ollama, emits a
// ScaleExplained K8s Event with the trimmed LLM output.
type Worker struct {
	Ollama        OllamaChatter
	EventRecorder record.EventRecorder
	Client        client.Client
	Config        WorkerConfig
}

// Run blocks until ctx is cancelled. Sequential — one Ollama call in
// flight at a time so a slow LLM cannot create unbounded goroutine fanout.
func (w *Worker) Run(ctx context.Context, ch <-chan controller.ExplainRequest) {
	logger := log.FromContext(ctx).WithValues("worker", "explainer")
	for {
		select {
		case <-ctx.Done():
			return
		case req, ok := <-ch:
			if !ok {
				return
			}
			w.handleRequest(ctx, logger, req)
		}
	}
}

// handleRequest runs the prompt build + Ollama call + event emit for one
// request. Wrapped in a panic-recovery boundary so a single bad request
// does not crash the goroutine.
func (w *Worker) handleRequest(ctx context.Context, logger logr.Logger, req controller.ExplainRequest) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error(fmt.Errorf("panic: %v", r), "ExplainWorker panicked",
				"namespace", req.Namespace, "name", req.Name)
			select {
			case <-ctx.Done():
			case <-time.After(PanicBackoff):
			}
		}
	}()

	sys, user := BuildPrompt(req)

	chatCtx, cancel := context.WithTimeout(ctx, w.Config.Timeout)
	defer cancel()

	content, err := w.Ollama.Chat(chatCtx, ollama.ChatRequest{
		Model: w.Config.Model,
		Messages: []ollama.ChatMessage{
			{Role: "system", Content: sys},
			{Role: "user", Content: user},
		},
		MaxTokens: w.Config.MaxTokens,
	})
	if err != nil {
		w.logOllamaErr(logger, err, req)
		return
	}

	trimmed := TrimContent(content, MaxEventLength)
	if trimmed == "" {
		// Defensive: an Ollama client that returns "" with nil error is
		// against the §9 contract, but we shouldn't emit a blank event
		// regardless.
		logger.Info("ollama returned empty content; skipping event",
			"namespace", req.Namespace, "name", req.Name)
		return
	}
	w.emitEvent(ctx, req, trimmed)
}

// logOllamaErr classifies the error per design §9 and logs at the right
// level. We do not emit any K8s Event on failure: the explanation is
// best-effort UX, not a scaling decision.
//
// Connection-refused is demoted to Info because the documented default
// in-cluster posture is "Ollama not configured" — config/manager/manager.yaml
// leaves OLLAMA_URL unset and the Go config substitutes
// http://localhost:11434, which is the controller pod's loopback where
// nothing is listening. Logging ECONNREFUSED at Error every reconcile
// drowns the controller logs in noise and trips alerting that watches
// for ERROR-level lines. Operators who *do* want explanations point
// OLLAMA_URL at a real Ollama and an actual failure surfaces in the
// default branch.
func (w *Worker) logOllamaErr(logger logr.Logger, err error, req controller.ExplainRequest) {
	switch {
	case errors.Is(err, ollama.ErrModelNotFound):
		logger.Info("ollama model not found; run `ollama pull <model>`",
			"namespace", req.Namespace, "name", req.Name,
			"model", w.Config.Model, "err", err.Error())
	case errors.Is(err, ollama.ErrEmptyResponse):
		logger.Info("ollama returned empty response; skipping event",
			"namespace", req.Namespace, "name", req.Name)
	case errors.Is(err, syscall.ECONNREFUSED):
		logger.Info("ollama unreachable (connection refused); explanations disabled. "+
			"Set OLLAMA_URL to a reachable endpoint to enable.",
			"namespace", req.Namespace, "name", req.Name)
	default:
		logger.Error(err, "ollama call failed",
			"namespace", req.Namespace, "name", req.Name)
	}
}

// emitEvent records a ScaleExplained Event on the CR. We Get the CR
// first so the EventRecorder has a real involvedObject; if the CR was
// deleted between Notify and now we silently skip.
//
// G22/F39: K8s Event Reason is PascalCase (kubectl convention); the
// snake_case token is prepended to the message body so log searches
// keyed on the canonical token still match. The body is re-trimmed
// so that prefix + content does not exceed MaxEventLength.
func (w *Worker) emitEvent(ctx context.Context, req controller.ExplainRequest, message string) {
	if w.EventRecorder == nil || w.Client == nil {
		return
	}
	var aas autoscalingv1alpha1.AgenticAutoscaler
	key := types.NamespacedName{Namespace: req.Namespace, Name: req.Name}
	if err := w.Client.Get(ctx, key, &aas); err != nil {
		return
	}
	prefix := reasoning.ScaleExplained + " "
	body := TrimContent(prefix+message, MaxEventLength)
	w.EventRecorder.Event(&aas, corev1.EventTypeNormal,
		reasoning.PascalReason(reasoning.ScaleExplained), body)
}
