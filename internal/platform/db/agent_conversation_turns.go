package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/misty-org/misty-server/internal/agent"
)

// A conversation's turns are its runs. These change which turns it has, for the
// actions a transcript offers: sending a prompt again drops it and what came after,
// and a Teach-mode reply can move into the lesson it belongs to.

// DetachAIConversationTurns takes finished turns out of a conversation, so the
// next run's history no longer has them. Their rows go when they expire, as with
// any run outside a conversation.
func (db *Database) DetachAIConversationTurns(ctx context.Context, userID, conversationID string, invocationIDs []string) error {
	if len(invocationIDs) == 0 {
		return nil
	}
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if err := requireOwnConversationTx(ctx, tx, userID, conversationID); err != nil {
			return err
		}
		ids := pq.Array(invocationIDs)
		if _, err := tx.ExecContext(ctx, `DELETE FROM misty_ask_conversation_events WHERE user_id=$1 AND conversation_id=$2 AND data->>'invocation_id'=ANY($3)`, userID, conversationID, ids); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET conversation_id=NULL,updated_at=NOW()
			WHERE user_id=$1 AND conversation_id=$2 AND id=ANY($3) AND state IN ('completed','failed','canceled')`, userID, conversationID, ids)
		return err
	})
}

// MoveAIConversationTurn moves one turn to another of the person's conversations.
func (db *Database) MoveAIConversationTurn(ctx context.Context, userID, conversationID, invocationID, toConversationID string) error {
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if err := requireOwnConversationTx(ctx, tx, userID, toConversationID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET conversation_id=$1,space_id=(SELECT space_id FROM misty_ask_conversations WHERE id=$1 AND user_id=$2),updated_at=NOW()
			WHERE id=$3 AND user_id=$2 AND conversation_id=$4`, toConversationID, userID, invocationID, conversationID)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return agent.ErrPersistedSessionNotFound
		}
		if _, err := tx.ExecContext(ctx, `UPDATE misty_ask_conversation_events SET conversation_id=$1 WHERE user_id=$2 AND conversation_id=$3 AND data->>'invocation_id'=$4`, toConversationID, userID, conversationID, invocationID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE ai_conversation_attachments SET conversation_id=$1 WHERE user_id=$2 AND conversation_id=$3 AND invocation_id=$4`, toConversationID, userID, conversationID, invocationID)
		return err
	})
}

func requireOwnConversationTx(ctx context.Context, tx *sql.Tx, userID, conversationID string) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM misty_ask_conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL)`, conversationID, userID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return agent.ErrPersistedSessionNotFound
	}
	return nil
}

// ReplaceAIConversationReply records a new text for a finished turn's reply: the
// requirements ledger riding on it changed. The newest assistant.message is the reply.
func (db *Database) ReplaceAIConversationReply(ctx context.Context, userID, conversationID, invocationID string, update func(current string) string) error {
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var state string
		err := tx.QueryRowContext(ctx, `SELECT state FROM ai_invocations WHERE id=$1 AND user_id=$2 AND conversation_id=$3 FOR UPDATE`, invocationID, userID, conversationID).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return agent.ErrPersistedSessionNotFound
		}
		if err != nil {
			return err
		}
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT payload->>'text' FROM ai_invocation_events WHERE invocation_id=$1 AND event_type='assistant.message' ORDER BY sequence DESC LIMIT 1),'')`, invocationID).Scan(&current); err != nil {
			return err
		}
		return appendStoredEventTx(ctx, tx, invocationID, "assistant.message", map[string]any{"type": "assistant.message", "text": update(current)}, "ledger:"+uuid.NewString(), nil)
	})
}

// appendStoredEventTx adds an event to a run's history outside its live stream:
// for a turn that has finished, or one uploaded from a computer's old local store.
// The caller holds the run's row.
func appendStoredEventTx(ctx context.Context, tx *sql.Tx, invocationID, eventType string, value map[string]any, receipt string, at any) error {
	var sequence int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM ai_invocation_events WHERE invocation_id=$1`, invocationID).Scan(&sequence); err != nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_invocation_events(invocation_id,sequence,event_type,payload,receipt_key,created_at) VALUES($1,$2,$3,$4,$5,COALESCE($6::timestamptz,NOW()))`, invocationID, sequence, eventType, encoded, receipt, at)
	return err
}
