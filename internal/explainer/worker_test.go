/*
Copyright 2026.
*/

package explainer_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	autoscalingv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/api/v1alpha1"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/adapters/ollama"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/controller"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/explainer"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/reasoning"
)

// -----------------------------------------------------------------------
// Shared helpers.
// -----------------------------------------------------------------------

type fakeOllama struct {
	mu       sync.Mutex
	response string
	err      error
	calls    int
	lastReq  ollama.ChatRequest
	delay    time.Duration
}

func (f *fakeOllama) Chat(ctx context.Context, req ollama.ChatRequest) (string, error) {
	f.mu.Lock()
	f.calls++
	f.lastReq = req
	delay := f.delay
	resp, err := f.response, f.err
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(delay):
		}
	}
	return resp, err
}

func (f *fakeOllama) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeOllama) LastRequest() ollama.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastReq
}

func newCR() *autoscalingv1alpha1.AgenticAutoscaler {
	return &autoscalingv1alpha1.AgenticAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "demo",
			Name:      "app-agentic",
		},
		Spec: autoscalingv1alpha1.AgenticAutoscalerSpec{
			TargetRef: autoscalingv1alpha1.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "app-agentic",
			},
		},
	}
}

func newWorker(t *testing.T, fakeChat *fakeOllama, cr *autoscalingv1alpha1.AgenticAutoscaler) (*explainer.Worker, *record.FakeRecorder) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, autoscalingv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	var cl client.Client = fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		Build()
	rec := record.NewFakeRecorder(16)
	return &explainer.Worker{
		Ollama:        fakeChat,
		EventRecorder: rec,
		Client:        cl,
		Config: explainer.WorkerConfig{
			Model:     "phi3",
			MaxTokens: 150,
			Timeout:   1 * time.Second,
		},
	}, rec
}

// -----------------------------------------------------------------------
// Happy path: Ollama is called once, event is emitted, content is trimmed.
// -----------------------------------------------------------------------

func TestWorker_HappyPath_CallsOllamaAndEmitsEvent(t *testing.T) {
	fake := &fakeOllama{response: "Traffic is increasing — scaling up to keep tail latency in check."}
	w, rec := newWorker(t, fake, newCR())

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan controller.ExplainRequest, 1)

	var done atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(ctx, ch)
		done.Store(true)
	}()

	ch <- baseRequest()

	require.Eventually(t, func() bool { return fake.CallCount() == 1 },
		800*time.Millisecond, 10*time.Millisecond)

	// Per F39, the Reason field is PascalCase ("ScaleExplained") and the
	// snake_case token is in the message body.
	require.Eventually(t, func() bool {
		select {
		case e := <-rec.Events:
			return strings.Contains(e, "ScaleExplained") &&
				strings.Contains(e, reasoning.ScaleExplained) &&
				strings.Contains(e, "scaling up")
		default:
			return false
		}
	}, 500*time.Millisecond, 10*time.Millisecond)

	cancel()
	wg.Wait()
	assert.True(t, done.Load())

	// Verify the request shape.
	req := fake.LastRequest()
	assert.Equal(t, "phi3", req.Model)
	assert.Equal(t, 150, req.MaxTokens)
	require.Len(t, req.Messages, 2)
	assert.Equal(t, "system", req.Messages[0].Role)
	assert.Equal(t, "user", req.Messages[1].Role)
	assert.False(t, req.Stream, "Stream must remain false")
}

// -----------------------------------------------------------------------
// Output longer than MaxEventLength is trimmed.
// -----------------------------------------------------------------------

func TestWorker_LongOutput_IsTrimmed(t *testing.T) {
	long := strings.Repeat("x", 600)
	fake := &fakeOllama{response: long}
	w, rec := newWorker(t, fake, newCR())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan controller.ExplainRequest, 1)

	go w.Run(ctx, ch)
	ch <- baseRequest()

	require.Eventually(t, func() bool {
		select {
		case e := <-rec.Events:
			// FakeRecorder formats as "<eventtype> <reason> <message>".
			// Find the message portion (after the second space group).
			parts := strings.SplitN(e, " ", 3)
			if len(parts) < 3 {
				return false
			}
			msg := parts[2]
			return len(msg) == explainer.MaxEventLength &&
				strings.HasSuffix(msg, "...")
		default:
			return false
		}
	}, 800*time.Millisecond, 10*time.Millisecond)
}

// -----------------------------------------------------------------------
// Failure path: Ollama returns generic error → log + continue, no event.
// -----------------------------------------------------------------------

func TestWorker_OllamaError_LogsAndContinues(t *testing.T) {
	fake := &fakeOllama{err: errors.New("connection refused")}
	w, rec := newWorker(t, fake, newCR())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan controller.ExplainRequest, 1)

	go w.Run(ctx, ch)
	ch <- baseRequest()

	require.Eventually(t, func() bool { return fake.CallCount() == 1 },
		500*time.Millisecond, 10*time.Millisecond)

	// Drain any background events for 100ms — none should have arrived.
	select {
	case e := <-rec.Events:
		t.Fatalf("unexpected event on Ollama error: %s", e)
	case <-time.After(100 * time.Millisecond):
	}
}

// -----------------------------------------------------------------------
// 404 → log warning, no event.
// -----------------------------------------------------------------------

func TestWorker_OllamaModelNotFound_LogsAndContinues(t *testing.T) {
	fake := &fakeOllama{err: ollama.ErrModelNotFound}
	w, rec := newWorker(t, fake, newCR())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan controller.ExplainRequest, 1)

	go w.Run(ctx, ch)
	ch <- baseRequest()

	require.Eventually(t, func() bool { return fake.CallCount() == 1 },
		500*time.Millisecond, 10*time.Millisecond)

	select {
	case e := <-rec.Events:
		t.Fatalf("unexpected event on model-not-found: %s", e)
	case <-time.After(100 * time.Millisecond):
	}
}

// -----------------------------------------------------------------------
// Empty response from Ollama → log, no event.
// -----------------------------------------------------------------------

func TestWorker_OllamaEmptyResponseSentinel_LogsAndContinues(t *testing.T) {
	fake := &fakeOllama{err: ollama.ErrEmptyResponse}
	w, rec := newWorker(t, fake, newCR())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan controller.ExplainRequest, 1)

	go w.Run(ctx, ch)
	ch <- baseRequest()

	require.Eventually(t, func() bool { return fake.CallCount() == 1 },
		500*time.Millisecond, 10*time.Millisecond)

	select {
	case e := <-rec.Events:
		t.Fatalf("unexpected event on empty-response sentinel: %s", e)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWorker_OllamaReturnsBlankString_NoEvent(t *testing.T) {
	// Pathological: nil error AND empty content. Defensive guard in worker.
	fake := &fakeOllama{response: ""}
	w, rec := newWorker(t, fake, newCR())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan controller.ExplainRequest, 1)

	go w.Run(ctx, ch)
	ch <- baseRequest()

	require.Eventually(t, func() bool { return fake.CallCount() == 1 },
		500*time.Millisecond, 10*time.Millisecond)

	select {
	case e := <-rec.Events:
		t.Fatalf("unexpected event on blank Ollama content: %s", e)
	case <-time.After(100 * time.Millisecond):
	}
}

// -----------------------------------------------------------------------
// Multiple events processed sequentially (one at a time).
// -----------------------------------------------------------------------

func TestWorker_MultipleEvents_ProcessedSequentially(t *testing.T) {
	fake := &fakeOllama{response: "ok"}
	w, _ := newWorker(t, fake, newCR())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan controller.ExplainRequest, 1)

	go w.Run(ctx, ch)

	for i := 0; i < 3; i++ {
		ch <- baseRequest()
	}

	require.Eventually(t, func() bool { return fake.CallCount() == 3 },
		1*time.Second, 10*time.Millisecond)
}

// -----------------------------------------------------------------------
// Ctx cancellation exits the loop without processing pending events.
// -----------------------------------------------------------------------

func TestWorker_ContextCancellationExits(t *testing.T) {
	fake := &fakeOllama{response: "ok"}
	w, _ := newWorker(t, fake, newCR())

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan controller.ExplainRequest, 1)

	var done atomic.Bool
	go func() {
		w.Run(ctx, ch)
		done.Store(true)
	}()

	cancel()
	require.Eventually(t, func() bool { return done.Load() },
		500*time.Millisecond, 10*time.Millisecond)
	assert.Equal(t, 0, fake.CallCount(), "no requests should have been processed")
}

// -----------------------------------------------------------------------
// Ollama timeout: Chat respects the per-call timeout from WorkerConfig.
// -----------------------------------------------------------------------

func TestWorker_RespectsConfiguredTimeout(t *testing.T) {
	fake := &fakeOllama{response: "slow", delay: 200 * time.Millisecond}
	w, _ := newWorker(t, fake, newCR())
	w.Config.Timeout = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan controller.ExplainRequest, 1)

	go w.Run(ctx, ch)
	ch <- baseRequest()

	// The Chat call returns ctx.Err() once the per-request timeout fires;
	// that's an error path, so no event is emitted, but Calls > 0.
	require.Eventually(t, func() bool { return fake.CallCount() == 1 },
		500*time.Millisecond, 10*time.Millisecond)
}

// -----------------------------------------------------------------------
// Panic recovery: one bad request should not crash the goroutine.
// -----------------------------------------------------------------------

type panickyOllama struct{ calls atomic.Int32 }

func (p *panickyOllama) Chat(_ context.Context, _ ollama.ChatRequest) (string, error) {
	p.calls.Add(1)
	panic("boom")
}

func TestWorker_PanicRecovery_AllowsNextRequest(t *testing.T) {
	prev := explainer.PanicBackoff
	explainer.PanicBackoff = 20 * time.Millisecond
	t.Cleanup(func() { explainer.PanicBackoff = prev })

	prom := &panickyOllama{}
	w, _ := newWorker(t, &fakeOllama{}, newCR())
	w.Ollama = prom

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan controller.ExplainRequest, 1)

	go w.Run(ctx, ch)

	ch <- baseRequest()
	ch <- baseRequest()

	require.Eventually(t, func() bool { return prom.calls.Load() >= 2 },
		1*time.Second, 10*time.Millisecond,
		"second request must be processed despite first panic")
}
