package agent

import (
	"context"
	"errors"
	"log"
	"time"
)

func (s *SessionStore) acquireEntry(ctx context.Context, id, userID string) *sessionEntry {
	s.mu.Lock()
	s.cleanupLocked()
	entry := s.sessions[id]
	if entry != nil {
		entry.active++
		s.mu.Unlock()
		return entry
	}
	s.mu.Unlock()

	if s.persist == nil {
		return nil
	}
	raw, err := s.persist.LoadAgentSession(ctx, id, userID)
	if err != nil {
		if !errors.Is(err, ErrPersistedSessionNotFound) {
			log.Printf("load persisted agent session %q: %v", id, err)
		}
		return nil
	}
	// Staleness here is measured against retention, not the in-memory ttl: ttl
	// only decides how long a session stays cached in this process, while a
	// persisted session stays resumable — including from another device — for as
	// long as it is retained.
	session, err := TestingUnmarshalPersistentSession(raw, id, userID)
	if err != nil || session.UpdatedAt.Before(s.TestingNow().Add(-conversationRetention)) {
		if err != nil && !errors.Is(err, ErrPersistedSessionNotFound) {
			log.Printf("decode persisted agent session %q: %v", id, err)
		}
		return nil
	}
	loaded := &sessionEntry{userID: userID, session: session, active: 1}
	s.mu.Lock()
	if existing := s.sessions[id]; existing != nil {
		existing.active++
		entry = existing
	} else {
		s.sessions[id] = loaded
		entry = loaded
	}
	s.mu.Unlock()
	return entry
}

func (s *SessionStore) persistCreatedSession(session *Session) {
	if s.persist == nil {
		return
	}
	state, err := TestingMarshalPersistentSession(session)
	if err == nil {
		err = s.persist.CreateAgentSession(context.Background(), session.ID, session.UserID, state, session.UpdatedAt.Add(s.ttl), session.UpdatedAt.Add(conversationRetention))
	}
	if err != nil {
		log.Printf("persist new agent session %q: %v", session.ID, err)
	}
}

func (s *SessionStore) persistUpdatedSession(ctx context.Context, session *Session, events []PersistedConversationEvent) {
	if s.persist == nil {
		return
	}
	state, err := TestingMarshalPersistentSession(session)
	if err == nil {
		err = s.persist.SaveAgentSession(ctx, session.ID, session.UserID, state, events, session.UpdatedAt.Add(s.ttl), session.UpdatedAt.Add(conversationRetention))
	}
	if err != nil {
		log.Printf("persist agent session %q: %v", session.ID, err)
	}
}

func (s *SessionStore) cleanupLocked() {
	cutoff := s.TestingNow().Add(-s.ttl)
	for id, entry := range s.sessions {
		if entry.active > 0 {
			continue
		}
		// An active provider call owns the entry lock. Skip it so cleanup never
		// blocks unrelated session creation or lookups behind network latency.
		if !entry.mu.TryLock() {
			continue
		}
		expired := entry.session.UpdatedAt.Before(cutoff)
		entry.mu.Unlock()
		if expired {
			delete(s.sessions, id)
		}
	}
}

func (s *Session) appendEvent(event AgentEvent) {
	s.nextSequence++
	event.Sequence = s.nextSequence
	event.CreatedAt = time.Now()
	s.Events = append(s.Events, event)
}

func cloneSession(session *Session) *Session {
	cloned := *session
	cloned.Messages = append([]Message(nil), session.Messages...)
	cloned.ToolResults = append([]ToolResult(nil), session.ToolResults...)
	cloned.Events = append([]AgentEvent(nil), session.Events...)
	cloned.KnownPaths = make(map[string]struct{}, len(session.KnownPaths))
	for path := range session.KnownPaths {
		cloned.KnownPaths[path] = struct{}{}
	}
	cloned.PendingToolRequests = make(map[string]string, len(session.PendingToolRequests))
	for id, name := range session.PendingToolRequests {
		cloned.PendingToolRequests[id] = name
	}
	return &cloned
}
