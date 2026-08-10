package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/agents"
)

type cancelableBlockingProvider struct {
	once    sync.Once
	entered chan struct{}
}

func (provider *cancelableBlockingProvider) ProviderName() string { return ProviderVercelAI }

func (provider *cancelableBlockingProvider) ModelName() string { return "private-model" }

func (provider *cancelableBlockingProvider) Next(request ModelRequest) (ModelResponse, error) {
	return provider.NextContext(context.Background(), request)
}

func (provider *cancelableBlockingProvider) NextContext(ctx context.Context, _ ModelRequest) (ModelResponse, error) {
	provider.once.Do(func() { close(provider.entered) })
	<-ctx.Done()
	return ModelResponse{}, ctx.Err()
}

func TestCancelInterruptsInFlightProviderAndReleasesReservation(t *testing.T) {
	provider := &cancelableBlockingProvider{entered: make(chan struct{})}
	meter := &recordingUsageMeter{}
	service := NewService(NewSessionStore(0), provider, WithUsageMeter(meter))
	session := service.CreateSession("user")
	done := make(chan error, 1)
	go func() {
		done <- service.SendMessage(session.ID, "user", AgentMessageRequest{UserMessage: "wait"})
	}()
	select {
	case <-provider.entered:
	case <-time.After(time.Second):
		t.Fatal("provider call did not start")
	}
	if err := service.Cancel(session.ID, "user"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("SendMessage() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not interrupt the provider call")
	}
	if meter.releases != 1 {
		t.Fatalf("released reservations = %d, want 1", meter.releases)
	}
	events, err := service.Events(session.ID, "user", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Message != "session canceled" {
		t.Fatalf("events = %#v", events)
	}
}

func TestToolResultCannotBeReplayed(t *testing.T) {
	service := NewService(NewSessionStore(0), MockProvider{})
	session := service.CreateSession("user")
	if err := service.SendMessage(session.ID, "user", AgentMessageRequest{
		Mode: ModeAuto, UserMessage: "organize files", Capabilities: ToolManifest{Tools: []ToolDefinition{{Name: ToolListDirectory, Risk: RiskRead}}},
	}); err != nil {
		t.Fatal(err)
	}
	events, err := service.Events(session.ID, "user", 0)
	if err != nil {
		t.Fatal(err)
	}
	request := events[1].ToolRequests[0]
	result := ToolResult{RequestID: request.ID, Name: request.Name, OK: true, Result: json.RawMessage(`{"entries":[]}`)}
	if err := service.SubmitToolResults(session.ID, "user", []ToolResult{result}); err != nil {
		t.Fatal(err)
	}
	if err := service.SubmitToolResults(session.ID, "user", []ToolResult{result}); err == nil || !strings.Contains(err.Error(), "outstanding") {
		t.Fatalf("replayed tool result error = %v", err)
	}
}

func TestAgentSessionOwnership(t *testing.T) {
	service := NewService(NewSessionStore(0), MockProvider{})
	session := service.CreateSession("user-1")
	if err := service.SendMessage(session.ID, "user-2", AgentMessageRequest{UserMessage: "hello"}); err != ErrSessionNotFound {
		t.Fatalf("SendMessage() error = %v, want ErrSessionNotFound", err)
	}
}

func TestSessionStoreTTLCleanup(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := NewSessionStore(time.Minute)
	store.TestingNow = func() time.Time { return now }

	session := store.Create("user-1")
	now = now.Add(2 * time.Minute)
	_ = store.Create("user-1")

	if _, err := store.Events(session.ID, "user-1", 0); err != ErrSessionNotFound {
		t.Fatalf("Events() error = %v, want ErrSessionNotFound after TTL cleanup", err)
	}
}

type blockingProvider struct {
	entered chan struct{}
	release chan struct{}
}

func (provider *blockingProvider) Next(ModelRequest) (ModelResponse, error) {
	provider.entered <- struct{}{}
	<-provider.release
	return ModelResponse{Text: "done"}, nil
}

func TestDifferentSessionsCanCallProviderConcurrently(t *testing.T) {
	provider := &blockingProvider{entered: make(chan struct{}, 2), release: make(chan struct{})}
	service := NewService(NewSessionStore(0), provider)
	first := service.CreateSession("user-1")
	second := service.CreateSession("user-2")
	done := make(chan error, 2)
	go func() { done <- service.SendMessage(first.ID, "user-1", AgentMessageRequest{UserMessage: "first"}) }()
	go func() { done <- service.SendMessage(second.ID, "user-2", AgentMessageRequest{UserMessage: "second"}) }()

	for range 2 {
		select {
		case <-provider.entered:
		case <-time.After(time.Second):
			close(provider.release)
			t.Fatal("provider calls for different sessions were serialized")
		}
	}
	close(provider.release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
