package agent

import (
	"context"
	"sync"
	"time"
)


import (
	"encoding/json"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/agents"
)

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
