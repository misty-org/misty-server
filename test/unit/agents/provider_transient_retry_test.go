package agent

import (
	"context"
	"sync/atomic"
	"testing"

	. "github.com/kannachi323/misty/server/internal/agents"
)

type timeoutOnceProvider struct {
	calls atomic.Int32
}

func (provider *timeoutOnceProvider) Next(request ModelRequest) (ModelResponse, error) {
	return provider.NextContext(context.Background(), request)
}

func (provider *timeoutOnceProvider) NextContext(_ context.Context, _ ModelRequest) (ModelResponse, error) {
	if provider.calls.Add(1) == 1 {
		return ModelResponse{}, context.DeadlineExceeded
	}
	return ModelResponse{Text: "Recovered after a transient timeout."}, nil
}

func TestAgentCompletionRetriesOneTransientProviderTimeout(t *testing.T) {
	provider := &timeoutOnceProvider{}
	service := NewService(nil, provider)
	text, _, err := service.CompleteWithTierContext(context.Background(), "user", "hello", "assistant_ai", TierLow)
	if err != nil {
		t.Fatal(err)
	}
	if text != "Recovered after a transient timeout." || provider.calls.Load() != 2 {
		t.Fatalf("text = %q, calls = %d", text, provider.calls.Load())
	}
}
