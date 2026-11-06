package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/lib/pq"

	"github.com/misty-org/misty-server/internal/agent"
)

var _ agent.SessionPersistence = (*Database)(nil)

// CreateAgentSession stores a conversation's state as the server's old agent
// loop did. Tests seed older conversations with it.
func (db *Database) CreateAgentSession(ctx context.Context, conversationID, userID string, state json.RawMessage, activeUntil, retentionExpiresAt time.Time) error {
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO misty_ask_conversations(id, user_id, state, active_until, retention_expires_at)
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
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT state
			FROM misty_ask_conversations
			WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL AND retention_expires_at > NOW()
		`, conversationID, userID).Scan(&state)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, agent.ErrPersistedSessionNotFound
	}
	return state, err
}

// AgentSessionSummary is the listing shape: enough to render a session rail
// without loading conversation state or events.
type AgentSessionSummary struct {
	HasLegacySession bool   `json:"-"`
	AgentID          string `json:"agent_id,omitempty"`
	FolderID         string `json:"folder_id,omitempty"`
	ID               string `json:"id"`
	Title            string `json:"title"`
	Active           bool   `json:"active"`

	SpaceID          string    `json:"space_id,omitempty"`
	ConversationKind string    `json:"kind"`
	OriginSurface    string    `json:"origin_surface,omitempty"`
	OriginHref       string    `json:"origin_href,omitempty"`
	PrivacyBoundary  string    `json:"-"`
	ModelID          string    `json:"model_id,omitempty"`
	ModelOverride    string    `json:"model_override,omitempty"`
	ReasoningEffort  string    `json:"reasoning_effort,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	// LastReadAt is when the user last saw the conversation; nil if never.
	LastReadAt *time.Time `json:"last_read_at,omitempty"`
	// PersonalSpace reports that SpaceID is the owner's personal Space, which a
	// window shows without knowing its id.
	PersonalSpace bool `json:"-"`
}

// ListAgentSessions returns the person's retained conversations in every Space,
// newest first. Conversations past active_until are still listed: they are
// resumable history, and only retention removes them. Active reports which ones
// the runtime can still continue. RLS and the owner predicate keep this to the
// conversation's owner; a Space's owner sees no one else's.
func (db *Database) ListAgentSessions(ctx context.Context, userID string) ([]AgentSessionSummary, error) {
	items := []AgentSessionSummary{}
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT c.id, c.title, c.active_until > NOW(), c.space_id,
				c.conversation_kind,c.origin_surface,c.origin_href,c.privacy_boundary,c.model_id,c.model_override,c.reasoning_effort,c.created_at,c.updated_at,COALESCE(c.agent_id,''),c.state <> '{}'::jsonb,COALESCE(c.folder_id,''),c.last_read_at,
				COALESCE((SELECT s.is_default FROM spaces s WHERE s.id = c.space_id AND s.owner_user_id = c.user_id), false)
			FROM misty_ask_conversations c
			WHERE c.user_id = $1 AND c.deleted_at IS NULL
			ORDER BY c.updated_at DESC
		`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentSessionSummary
			if err := rows.Scan(&item.ID, &item.Title, &item.Active, &item.SpaceID, &item.ConversationKind, &item.OriginSurface, &item.OriginHref, &item.PrivacyBoundary, &item.ModelID, &item.ModelOverride, &item.ReasoningEffort, &item.CreatedAt, &item.UpdatedAt, &item.AgentID, &item.HasLegacySession, &item.FolderID, &item.LastReadAt, &item.PersonalSpace); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// AgentSessionContext is the Space and Agent a session was bound to when it was
// created. It is read from the session row rather than taken from the request,
// so a caller cannot point an existing session at a different Space.
type AgentSessionContext struct {
	AgentID         string
	SpaceID         string
	ModelID         string
	ReasoningEffort string
	// ModelOverride is the model the user picked for this conversation; empty
	// follows the account's Thinking choice.
	ModelOverride string
}

func (db *Database) AgentConversationIdentity(ctx context.Context, userID, conversationID string) (AgentSessionContext, error) {
	var bound AgentSessionContext
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT space_id,model_id,reasoning_effort,COALESCE(agent_id,''),model_override
			FROM misty_ask_conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, conversationID, userID).
			Scan(&bound.SpaceID, &bound.ModelID, &bound.ReasoningEffort, &bound.AgentID, &bound.ModelOverride)
		if errors.Is(err, sql.ErrNoRows) {
			return agent.ErrPersistedSessionNotFound
		}
		return err
	})
	return bound, err
}

// RenameAgentSession sets the human-facing label. Clients derive a first title
// from the opening message, so this runs on the first exchange and again on any
// explicit rename.
func (db *Database) RenameAgentSession(ctx context.Context, userID, conversationID, title string) error {
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE misty_ask_conversations
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
			return agent.ErrPersistedSessionNotFound
		}
		return nil
	})
}

// TitleAgentSessionIfUnchanged sets a generated title only while the
// conversation still has one of its placeholder titles, so a rename by the
// person, on any device, always wins. It reports whether the title changed.
func (db *Database) TitleAgentSessionIfUnchanged(ctx context.Context, userID, conversationID, title string, placeholders []string) (bool, error) {
	changed := false
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE misty_ask_conversations
			SET title = $1
			WHERE id = $2 AND user_id = $3 AND deleted_at IS NULL AND title = ANY($4) AND title <> $1
		`, title, conversationID, userID, pq.Array(placeholders))
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		changed = rows > 0
		return err
	})
	return changed, err
}

// MarkAgentConversationRead records that the user has seen the conversation as of
// now. It leaves updated_at alone, so reading never reorders the sidebar.
func (db *Database) MarkAgentConversationRead(ctx context.Context, userID, conversationID string) (time.Time, error) {
	var readAt time.Time
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `
			UPDATE misty_ask_conversations SET last_read_at = NOW()
			WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
			RETURNING last_read_at
		`, conversationID, userID).Scan(&readAt)
		if errors.Is(err, sql.ErrNoRows) {
			return agent.ErrPersistedSessionNotFound
		}
		return err
	})
	return readAt, err
}

// DeleteAgentConversation is the user-facing retention escape hatch. The
// conversation's turns, tool output and event data go now, not at the next sweep.
func (db *Database) DeleteAgentConversation(ctx context.Context, userID, conversationID string) error {
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return deleteAgentConversationsTx(ctx, tx, userID, []string{conversationID})
	})
}

// deleteAgentConversationsTx removes one person's conversations with everything
// stored for them. A run whose artifact was applied keeps its row as the record
// of that effect, but its prompt goes and it leaves the conversation.
func deleteAgentConversationsTx(ctx context.Context, tx *sql.Tx, userID string, conversationIDs []string) error {
	ids := pq.Array(conversationIDs)
	// Collaboration rows (mode, questions, plans, goals) go with their conversation.
	for _, table := range []string{"ai_conversation_modes", "agent_question_sets", "agent_plans", "agent_goals"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE owner_user_id=$1 AND conversation_id=ANY($2)`, userID, ids); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_lesson_documents WHERE owner_user_id=$1 AND kind='lesson' AND id=ANY($2)`, userID, ids); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM ai_invocations i WHERE i.user_id=$1 AND i.conversation_id=ANY($2)
		AND i.state IN ('completed','failed','canceled')
		AND NOT EXISTS(SELECT 1 FROM ai_artifacts a WHERE a.invocation_id=i.id AND a.state='applied')`, userID, ids); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET conversation_id=NULL,request_payload='{}'::jsonb,updated_at=NOW() WHERE user_id=$1 AND conversation_id=ANY($2) AND state IN ('completed','failed','canceled')`, userID, ids); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM misty_ask_conversations WHERE id=ANY($1) AND user_id=$2`, ids, userID)
	return err
}
