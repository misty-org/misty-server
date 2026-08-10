package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var ErrSessionNotFound = errors.New("ai session not found")

type SessionStore struct {
	mu         sync.Mutex
	TestingNow func() time.Time
	ttl        time.Duration
	sessions   map[string]*sessionEntry
	persist    SessionPersistence
}

type sessionEntry struct {
	mu       sync.Mutex
	cancelMu sync.Mutex
	cancel   context.CancelFunc
	userID   string
	session  *Session
	active   int
}

type Session struct {
	ID                    string
	UserID                string
	BillingUserID         string
	BillingScope          string
	ModelID               string
	ReasoningEffort       string
	SystemPrompt          string
	AllowTools            bool
	AllowWriteTools       bool
	AgentTier             AgentTier
	Mode                  string
	ActiveRoot            string
	Capabilities          ToolManifest
	Messages              []Message
	ToolResults           []ToolResult
	KnownPaths            map[string]struct{}
	Events                []AgentEvent
	nextSequence          int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
	Canceled              bool
	ProviderCallsThisTurn int
	PendingToolRequests   map[string]string

	// SpaceID is the Space this session is bound to, copied from the session row
	// rather than from a request body.
	SpaceID string
	// SpaceCard describes the Space and the member's permissions there. Stable
	// for the conversation, so it lives in the cacheable prompt prefix.
	SpaceCard string
	// SpaceRecords is the permission-filtered Space content for the current
	// turn, rebuilt whenever SpaceContextRevision changes.
	SpaceRecords string
	// SpaceContextRevision is the change token the records were built from.
	// Equal token means nothing the agent can see has changed, so the records
	// are reused and the prompt bytes stay identical turn to turn.
	SpaceContextRevision string
	// SpaceSection is the surface the member was working in on the last turn.
	SpaceSection string
}

func NewSessionStore(ttl time.Duration) *SessionStore {
	return NewSessionStoreWithPersistence(ttl, nil)
}

func NewSessionStoreWithPersistence(ttl time.Duration, persistence SessionPersistence) *SessionStore {
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	return &SessionStore{
		TestingNow: time.Now,
		ttl:        ttl,
		sessions:   make(map[string]*sessionEntry),
		persist:    persistence,
	}
}

func (s *SessionStore) Create(userID string) *Session {
	return s.CreateWithBilling(userID, userID)
}

func (s *SessionStore) CreateWithBilling(userID, billingUserID string) *Session {
	return s.CreateWithBillingScope(userID, billingUserID, "")
}

func (s *SessionStore) CreateWithModel(userID, billingUserID, modelID string) *Session {
	session := s.CreateWithBillingScope(userID, billingUserID, "")
	_ = s.WithSession(session.ID, userID, func(current *Session) error {
		current.ModelID = strings.TrimSpace(modelID)
		return nil
	})
	session.ModelID = strings.TrimSpace(modelID)
	return session
}

func (s *SessionStore) CreateWithBillingScope(userID, billingUserID, billingScope string) *Session {
	s.mu.Lock()
	s.cleanupLocked()
	now := s.TestingNow()
	session := &Session{
		ID:                  "conversation_" + uuid.NewString(),
		UserID:              userID,
		BillingUserID:       billingUserID,
		BillingScope:        billingScope,
		AgentTier:           TierLow,
		Mode:                ModeAsk,
		AllowTools:          true,
		AllowWriteTools:     true,
		KnownPaths:          make(map[string]struct{}),
		PendingToolRequests: make(map[string]string),
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	s.sessions[session.ID] = &sessionEntry{userID: userID, session: session}
	s.mu.Unlock()
	s.persistCreatedSession(session)
	return cloneSession(session)
}

func (s *SessionStore) WithSession(id, userID string, fn func(session *Session) error) error {
	return s.WithSessionContext(context.Background(), id, userID, func(_ context.Context, session *Session) error {
		return fn(session)
	})
}

func (s *SessionStore) WithSessionContext(ctx context.Context, id, userID string, fn func(context.Context, *Session) error) error {
	entry := s.acquireEntry(ctx, id, userID)
	if entry == nil {
		return ErrSessionNotFound
	}
	defer s.releaseEntry(entry)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.session.UserID != userID {
		return ErrSessionNotFound
	}
	requestCtx, cancel := context.WithCancel(ctx)
	entry.cancelMu.Lock()
	entry.cancel = cancel
	entry.cancelMu.Unlock()
	defer func() {
		entry.cancelMu.Lock()
		entry.cancel = nil
		entry.cancelMu.Unlock()
		cancel()
	}()
	beforeMessages := len(entry.session.Messages)
	beforeResults := len(entry.session.ToolResults)
	beforeEvents := len(entry.session.Events)
	if err := fn(requestCtx, entry.session); err != nil {
		return err
	}
	entry.session.UpdatedAt = s.TestingNow()
	s.persistUpdatedSession(ctx, entry.session, persistentEvents(beforeMessages, beforeResults, beforeEvents, entry.session))
	return nil
}

func (s *SessionStore) Events(id, userID string, after int64) ([]AgentEvent, error) {
	entry := s.acquireEntry(context.Background(), id, userID)
	if entry == nil {
		return nil, ErrSessionNotFound
	}
	defer s.releaseEntry(entry)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	session := entry.session
	if session.UserID != userID {
		return nil, ErrSessionNotFound
	}
	events := make([]AgentEvent, 0, len(session.Events))
	for _, event := range session.Events {
		if event.Sequence > after {
			events = append(events, event)
		}
	}
	return events, nil
}

func (s *SessionStore) releaseEntry(entry *sessionEntry) {
	s.mu.Lock()
	entry.active--
	s.mu.Unlock()
}

func (s *SessionStore) Cancel(id, userID string) error {
	entry := s.acquireEntry(context.Background(), id, userID)
	if entry == nil || entry.userID != userID {
		if entry != nil {
			s.releaseEntry(entry)
		}
		return ErrSessionNotFound
	}
	defer s.releaseEntry(entry)

	entry.cancelMu.Lock()
	if entry.cancel != nil {
		entry.cancel()
	}
	entry.cancelMu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()
	beforeEvents := len(entry.session.Events)
	if !entry.session.Canceled {
		entry.session.Canceled = true
		entry.session.appendEvent(AgentEvent{Type: EventError, Message: "session canceled"})
	}
	entry.session.UpdatedAt = s.TestingNow()
	s.persistUpdatedSession(context.Background(), entry.session, persistentEvents(len(entry.session.Messages), len(entry.session.ToolResults), beforeEvents, entry.session))
	return nil
}

// Forget removes an owned session from process memory after its durable
// conversation has been deleted by the account owner.
func (s *SessionStore) Forget(id, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.sessions[id]
	if entry == nil || entry.userID != userID {
		return ErrSessionNotFound
	}
	entry.cancelMu.Lock()
	if entry.cancel != nil {
		entry.cancel()
	}
	entry.cancelMu.Unlock()
	delete(s.sessions, id)
	return nil
}
