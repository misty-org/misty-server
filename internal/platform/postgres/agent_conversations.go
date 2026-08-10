package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
)

var _ serveragent.SessionPersistence = (*Database)(nil)

// CreateAgentSession implements agent.SessionPersistence. Conversation state
// is sanitized by the agent package before it crosses this boundary.
func (db *Database) CreateAgentSession(ctx context.Context, conversationID, userID string, state json.RawMessage, activeUntil, retentionExpiresAt time.Time) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO agent_conversations(id, user_id, state, active_until, retention_expires_at)
			VALUES($1, $2, $3, $4, $5)
			ON CONFLICT(id) DO NOTHING
		`, conversationID, userID, state, activeUntil, retentionExpiresAt)
		return err
	})
}

// LoadAgentSession returns owned sessions that are still retained. RLS and the
// explicit user predicate ensure callers cannot probe another account's
// conversation.
//
// This gates on retention rather than active_until: active_until is the
// in-memory cache TTL, and gating resumption on it meant a session opened on one
// device could not be continued from another two hours later, which defeats the
// point of persisting it for thirty days.
func (db *Database) LoadAgentSession(ctx context.Context, conversationID, userID string) (json.RawMessage, error) {
	var state json.RawMessage
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT state
			FROM agent_conversations
			WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL AND retention_expires_at > NOW()
		`, conversationID, userID).Scan(&state)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, serveragent.ErrPersistedSessionNotFound
	}
	return state, err
}

// SaveAgentSession atomically updates the resumable state and appends its
// durable event records. A failed transaction leaves neither half committed.
func (db *Database) SaveAgentSession(ctx context.Context, conversationID, userID string, state json.RawMessage, events []serveragent.PersistedConversationEvent, activeUntil, retentionExpiresAt time.Time) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE agent_conversations
			SET state = $1, active_until = $2, retention_expires_at = $3, updated_at = NOW()
			WHERE id = $4 AND user_id = $5 AND deleted_at IS NULL
		`, state, activeUntil, retentionExpiresAt, conversationID, userID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return serveragent.ErrPersistedSessionNotFound
		}
		for _, event := range events {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO agent_conversation_events(conversation_id, user_id, event_type, data)
				VALUES($1, $2, $3, $4)
			`, conversationID, userID, event.Type, event.Data); err != nil {
				return err
			}
		}
		return nil
	})
}

// AgentSessionSummary is the listing shape: enough to render a session rail
// without loading conversation state or events.
type AgentSessionSummary struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Active          bool      `json:"active"`
	PersonalAgentID string    `json:"agent_id,omitempty"`
	SpaceID         string    `json:"space_id,omitempty"`
	ModelID         string    `json:"model_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ListAgentSessions returns the account's retained sessions, newest first.
// Sessions past active_until are still listed — they are resumable history, and
// only retention_expires_at removes them — with Active reporting which ones the
// runtime can still continue.
func (db *Database) ListAgentSessions(ctx context.Context, userID string) ([]AgentSessionSummary, error) {
	items := []AgentSessionSummary{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, title, active_until > NOW(), COALESCE(personal_agent_id,''), COALESCE(space_id,''), model_id, created_at, updated_at
			FROM agent_conversations
			WHERE user_id = $1 AND deleted_at IS NULL
			ORDER BY updated_at DESC
		`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentSessionSummary
			if err := rows.Scan(&item.ID, &item.Title, &item.Active, &item.PersonalAgentID, &item.SpaceID, &item.ModelID, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) BindAgentSessionContext(ctx context.Context, userID, conversationID, agentID, spaceID, modelID, catalogVersion string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE agent_conversations SET personal_agent_id=NULLIF($1,''),space_id=NULLIF($2,''),model_id=$3,model_catalog_version=$4,updated_at=NOW() WHERE id=$5 AND user_id=$6 AND deleted_at IS NULL`, agentID, spaceID, modelID, catalogVersion, conversationID, userID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return serveragent.ErrPersistedSessionNotFound
		}
		return nil
	})
}

// ValidateAgentSpaceAccess checks the permissions required to create a Space-
// scoped Agent session. Personal Agent sessions additionally require a current
// owner or sharing grant for the Agent in that Space.
func (db *Database) ValidateAgentSpaceAccess(ctx context.Context, userID, spaceID, agentID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return validateAgentSpaceAccessTx(ctx, tx, userID, spaceID, agentID)
	})
}

// AgentSessionContext is the Space and Agent a session was bound to when it was
// created. It is read from the session row rather than taken from the request,
// so a caller cannot point an existing session at a different Space.
type AgentSessionContext struct {
	SpaceID string
	AgentID string
	ModelID string
}

// ValidateAgentSessionAccess revalidates a persisted session's Space access and
// returns the context the session is bound to. This must run before every
// operation that can read context or continue a run, because Space membership,
// permissions, and Agent grants can change after the session was created.
//
// Callers must use the returned SpaceID rather than any space identifier from
// the request body. The client sends one, but trusting it would let a member
// with access to one Space read another Space's context through a session bound
// elsewhere.
func (db *Database) ValidateAgentSessionAccess(ctx context.Context, userID, conversationID string) (AgentSessionContext, error) {
	var bound AgentSessionContext
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		bound = AgentSessionContext{}
		err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(personal_agent_id, ''), COALESCE(space_id, ''), model_id
			FROM agent_conversations
			WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL
		`, conversationID, userID).Scan(&bound.AgentID, &bound.SpaceID, &bound.ModelID)
		if errors.Is(err, sql.ErrNoRows) {
			return serveragent.ErrPersistedSessionNotFound
		}
		if err != nil || bound.SpaceID == "" {
			return err
		}
		return validateAgentSpaceAccessTx(ctx, tx, userID, bound.SpaceID, bound.AgentID)
	})
	if err != nil {
		return AgentSessionContext{}, err
	}
	return bound, nil
}

func validateAgentSpaceAccessTx(ctx context.Context, tx *sql.Tx, userID, spaceID, agentID string) error {
	if agentID != "" {
		_, err := personalAgentAllowedTx(ctx, tx, userID, spaceID, agentID)
		return err
	}
	if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
		return err
	}
	return requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAgentsRun)
}

// RenameAgentSession sets the human-facing label. Clients derive a first title
// from the opening message, so this runs on the first exchange and again on any
// explicit rename.
func (db *Database) RenameAgentSession(ctx context.Context, userID, conversationID, title string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE agent_conversations
			SET title = $1
			WHERE id = $2 AND user_id = $3 AND deleted_at IS NULL
		`, title, conversationID, userID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return serveragent.ErrPersistedSessionNotFound
		}
		return nil
	})
}

// DeleteAgentConversation is the user-facing retention escape hatch. The
// cascade removes event data immediately rather than waiting for the sweeper.
func (db *Database) DeleteAgentConversation(ctx context.Context, userID, conversationID string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM agent_conversations WHERE id=$1 AND user_id=$2`, conversationID, userID)
		return err
	})
}

// PurgeExpiredAgentConversations is suitable for a periodic server task. It
// intentionally runs as the service role because expiration spans accounts.
func (db *Database) PurgeExpiredAgentConversations(ctx context.Context) (int64, error) {
	var deleted int64
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM agent_conversations WHERE retention_expires_at <= NOW()`)
		if err != nil {
			return err
		}
		deleted, err = result.RowsAffected()
		return err
	})
	return deleted, err
}
