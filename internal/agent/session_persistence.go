package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var ErrSessionNotFound = errors.New("ai session not found")

// ErrPersistedSessionNotFound allows persistence implementations to hide
// whether a conversation exists or belongs to another user.
var ErrPersistedSessionNotFound = errors.New("persisted ai session not found")

const conversationRetention = 30 * 24 * time.Hour

// SessionPersistence is where the server's old agent loop kept its
// conversations (misty_ask_conversations).
type SessionPersistence interface {
	LoadAgentSession(ctx context.Context, conversationID, userID string) (json.RawMessage, error)
}

// persistedSessionState is the part of a stored conversation a transcript reads.
type persistedSessionState struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	Messages  []Message `json:"messages"`
	UpdatedAt time.Time `json:"updatedAt"`
}
