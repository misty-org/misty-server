package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/kannachi323/misty/server/internal/agent"
)

// ErrConversationKeptElsewhere: another of the person's devices keeps this chat.
var ErrConversationKeptElsewhere = errors.New("conversation is kept on another device")

// KeepConversationOnDevice records the device that keeps a chat from now on: the
// one that started it, or the first to take it from the inbox. Once a device keeps
// it, the chat's runs are no longer retained past their own expiry, since the device
// holds the transcript. A chat another device keeps is refused.
func (db *Database) KeepConversationOnDevice(ctx context.Context, userID, conversationID, deviceID string) error {
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var kept sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT kept_on_device_id FROM misty_ask_conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL FOR UPDATE`, conversationID, userID).Scan(&kept)
		if errors.Is(err, sql.ErrNoRows) {
			return agent.ErrPersistedSessionNotFound
		}
		if err != nil {
			return err
		}
		if kept.Valid && kept.String != deviceID {
			return ErrConversationKeptElsewhere
		}
		_, err = tx.ExecContext(ctx, `UPDATE misty_ask_conversations SET kept_on_device_id=$3 WHERE id=$1 AND user_id=$2`, conversationID, userID, deviceID)
		return err
	})
}

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

// SetMistyConversationModelOverride pins a conversation to a model, or with an
// empty model returns it to the account's Thinking choice.
func (db *Database) SetMistyConversationModelOverride(ctx context.Context, userID, conversationID, model string) error {
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE misty_ask_conversations SET model_override=$1,updated_at=NOW() WHERE id=$2 AND user_id=$3 AND deleted_at IS NULL`, model, conversationID, userID)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil {
			return err
		} else if rows == 0 {
			return agent.ErrPersistedSessionNotFound
		}
		return nil
	})
}

// BindMistyConversationSpace establishes the immutable collaborative boundary
// for a built-in Misty conversation. An unbound account conversation may be
// attached once; subsequent calls are idempotent only for the same Space.
func (db *Database) BindMistyConversationSpace(ctx context.Context, userID, conversationID, spaceID string) error {
	// Compatibility adapter for old clients. Verify the owner but never bind.
	_, err := db.AgentConversationIdentity(ctx, userID, conversationID)
	return err
}

func (db *Database) BindAskSurfaceConversation(ctx context.Context, userID, conversationID, spaceID, modelID, surfaceID, originHref, privacyBoundary string) error {
	spaceID = ""
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE misty_ask_conversations SET
			space_id=NULLIF($1,''),model_id=$2,
			conversation_kind='misty',origin_surface=$3,origin_href=$4,privacy_boundary=$5,
			updated_at=NOW() WHERE id=$6 AND user_id=$7 AND deleted_at IS NULL`,
			spaceID, modelID, surfaceID, originHref, privacyBoundary, conversationID, userID)
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
