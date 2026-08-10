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

type memorySessionPersistence struct {
	mu     sync.Mutex
	states map[string]json.RawMessage
	owners map[string]string
	events map[string][]PersistedConversationEvent
}

func newMemorySessionPersistence() *memorySessionPersistence {
	return &memorySessionPersistence{
		states: make(map[string]json.RawMessage),
		owners: make(map[string]string),
		events: make(map[string][]PersistedConversationEvent),
	}
}

func (p *memorySessionPersistence) CreateAgentSession(_ context.Context, id, userID string, state json.RawMessage, _, _ time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.states[id] = append(json.RawMessage(nil), state...)
	p.owners[id] = userID
	return nil
}

func (p *memorySessionPersistence) LoadAgentSession(_ context.Context, id, userID string) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owners[id] != userID {
		return nil, ErrPersistedSessionNotFound
	}
	state, ok := p.states[id]
	if !ok {
		return nil, ErrPersistedSessionNotFound
	}
	return append(json.RawMessage(nil), state...), nil
}

func (p *memorySessionPersistence) SaveAgentSession(_ context.Context, id, userID string, state json.RawMessage, events []PersistedConversationEvent, _, _ time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owners[id] != userID {
		return ErrPersistedSessionNotFound
	}
	p.states[id] = append(json.RawMessage(nil), state...)
	p.events[id] = append(p.events[id], events...)
	return nil
}

func TestUnmarshalPersistentSessionReadsCurrentTier(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		state string
		want  AgentTier
	}{
		{"high", `{"id":"s1","userId":"u1","agentTier":"tier-high"}`, TierHigh},
		{"current key only", `{"id":"s1","userId":"u1","agentTier":"tier-med"}`, TierMed},
		{"absent defaults low", `{"id":"s1","userId":"u1"}`, TierLow},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			session, err := TestingUnmarshalPersistentSession(json.RawMessage(testCase.state), "s1", "u1")
			if err != nil {
				t.Fatalf("unmarshalPersistentSession() error = %v", err)
			}
			if session.AgentTier != testCase.want {
				t.Fatalf("AgentTier = %q, want %q", session.AgentTier, testCase.want)
			}
		})
	}
}

// Only the current key is ever written, so migrated rows stop carrying the alias.
func TestMarshalPersistentSessionWritesOnlyCurrentTierKey(t *testing.T) {
	raw, err := TestingMarshalPersistentSession(&Session{ID: "s1", UserID: "u1", AgentTier: TierHigh})
	if err != nil {
		t.Fatalf("marshalPersistentSession() error = %v", err)
	}
	if strings.Contains(string(raw), "mikaTier") {
		t.Fatalf("persisted state still writes the legacy tier key: %s", raw)
	}
	if !strings.Contains(string(raw), `"agentTier":"tier-high"`) {
		t.Fatalf("persisted state missing current tier key: %s", raw)
	}
}

func TestPersistentSessionRestoresAfterRuntimeRestart(t *testing.T) {
	persistence := newMemorySessionPersistence()
	first := NewService(NewSessionStoreWithPersistence(time.Hour, persistence), MockProvider{})
	session := first.CreateSession("user-1")
	if !strings.HasPrefix(session.ID, "conversation_") {
		t.Fatalf("session ID = %q, want conversation_ prefix", session.ID)
	}
	if err := first.SendMessage(session.ID, "user-1", AgentMessageRequest{UserMessage: "hello"}); err != nil {
		t.Fatal(err)
	}

	second := NewService(NewSessionStoreWithPersistence(time.Hour, persistence), MockProvider{})
	events, err := second.Events(session.ID, "user-1", 0)
	if err != nil {
		t.Fatalf("Events after restart: %v", err)
	}
	if len(events) != 1 || events[0].Type != EventAgentMessage {
		t.Fatalf("restored events = %#v", events)
	}
	if err := second.SendMessage(session.ID, "user-1", AgentMessageRequest{UserMessage: "again"}); err != nil {
		t.Fatalf("SendMessage after restart: %v", err)
	}
	events, err = second.Events(session.ID, "user-1", 1)
	if err != nil || len(events) != 1 || events[0].Sequence != 2 {
		t.Fatalf("continued events = %#v, err = %v", events, err)
	}
	if _, err := second.Events(session.ID, "user-2", 0); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("cross-user Events error = %v", err)
	}
}

func TestPersistentSessionStripsImagesAndLocalPaths(t *testing.T) {
	persistence := newMemorySessionPersistence()
	store := NewSessionStoreWithPersistence(time.Hour, persistence)
	session := store.Create("user-1")
	err := store.WithSession(session.ID, "user-1", func(current *Session) error {
		current.ActiveRoot = "/Users/misty/Documents/private"
		current.KnownPaths["/Users/misty/Documents/private/report.pdf"] = struct{}{}
		current.Messages = append(current.Messages, Message{Role: RoleUser, Content: "read /Users/misty/Documents/private/report.pdf"})
		current.ToolResults = append(current.ToolResults, ToolResult{
			RequestID: "request-1",
			Name:      ToolPreviewFile,
			OK:        true,
			Result: json.RawMessage(`{
				"path":"/Users/misty/Documents/private/report.pdf",
				"relativePath":"report.pdf",
				"imageDataUrl":"data:image/jpeg;base64,secret",
				"sections":[{"text":"safe"}]
			}`),
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	persistence.mu.Lock()
	state := string(persistence.states[session.ID])
	persistence.mu.Unlock()
	for _, forbidden := range []string{"/Users/misty", "imageDataUrl", "base64,secret", `"text":"safe"`, "ActiveRoot", "KnownPaths"} {
		if strings.Contains(state, forbidden) {
			t.Fatalf("persisted state contains %q: %s", forbidden, state)
		}
	}
	if !strings.Contains(state, `relativePath`) || !strings.Contains(state, `report.pdf`) {
		t.Fatalf("persisted state lost safe citation coordinate: %s", state)
	}
}
