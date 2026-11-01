package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Service reads back the conversations the server's old agent loop kept in
// misty_ask_conversations. Agents now run on the person's desktop
// (docs/agents/runtime.md); the server never runs a model turn.
type Service struct {
	persist SessionPersistence
}

func NewService(persist SessionPersistence) *Service {
	return &Service{persist: persist}
}

// Transcript returns a stored conversation as plain messages, for a client
// rebuilding a conversation it does not hold locally.
func (s *Service) Transcript(ctx context.Context, sessionID, userID string) ([]Message, error) {
	if s == nil || s.persist == nil {
		return nil, ErrSessionNotFound
	}
	raw, err := s.persist.LoadAgentSession(ctx, sessionID, userID)
	if err != nil {
		if errors.Is(err, ErrPersistedSessionNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	var state persistedSessionState
	if json.Unmarshal(raw, &state) != nil || state.ID != sessionID || state.UserID != userID || state.UpdatedAt.Before(time.Now().Add(-conversationRetention)) {
		return nil, ErrSessionNotFound
	}
	return state.Messages, nil
}
