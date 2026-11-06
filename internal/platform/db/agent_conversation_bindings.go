package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/misty-org/misty-server/internal/agent"
)

func (db *Database) UpdateMistyConversationModel(ctx context.Context, userID, conversationID, modelID, reasoningEffort, catalogVersion string) error {
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE misty_ask_conversations SET model_id=$1,reasoning_effort=$2,model_catalog_version=$3,updated_at=NOW() WHERE id=$4 AND user_id=$5 AND deleted_at IS NULL`, modelID, reasoningEffort, catalogVersion, conversationID, userID)
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

// AgentConversationSpace is the Space a conversation belongs to. A conversation
// keeps its Space for its whole life, whichever window continues it.
func (db *Database) AgentConversationSpace(ctx context.Context, userID, conversationID string) (string, error) {
	var spaceID string
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT space_id FROM misty_ask_conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, conversationID, userID).Scan(&spaceID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", agent.ErrPersistedSessionNotFound
	}
	return spaceID, err
}

func (db *Database) BindAskSurfaceConversation(ctx context.Context, userID, conversationID, modelID, surfaceID, originHref, privacyBoundary string) error {
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE misty_ask_conversations SET
			model_id=$1,
			conversation_kind='misty',origin_surface=$2,origin_href=$3,privacy_boundary=$4,
			updated_at=NOW() WHERE id=$5 AND user_id=$6 AND deleted_at IS NULL`,
			modelID, surfaceID, originHref, privacyBoundary, conversationID, userID)
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
