package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (db *Database) UpdateSpaceSlackLinkDirection(ctx context.Context, userID, spaceID, linkID, direction string) (*SpaceSlackLink, error) {
	if !oneOf(direction, "two_way", "inbound", "outbound") {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceSlackLink{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		err := scanSpaceSlackLink(tx.QueryRowContext(ctx, `UPDATE space_slack_links
			SET direction=$1,updated_at=NOW() WHERE id=$2 AND space_id=$3 AND disabled_at IS NULL
			RETURNING `+spaceSlackLinkColumns, direction, linkID, spaceID), out)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		return err
	})
	return out, err
}

func (db *Database) SetSpaceSlackLinkSync(ctx context.Context, linkID, cursor, status, errorCode, botUserID string, syncedAt *time.Time) error {
	if !oneOf(status, "pending", "syncing", "active", "needs_attention", "disabled") {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_slack_links SET
			last_message_ts=CASE WHEN $2<>'' THEN $2 ELSE last_message_ts END,
			status=$3,last_error_code=$4,
			bot_user_id=CASE WHEN $5<>'' THEN $5 ELSE bot_user_id END,
			last_synced_at=COALESCE($6,last_synced_at),updated_at=NOW() WHERE id=$1`,
			linkID, cursor, status, errorCode, botUserID, syncedAt)
		return err
	})
}

func (db *Database) DeleteSpaceSlackLink(ctx context.Context, userID, spaceID, linkID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		var conversationID string
		if err := tx.QueryRowContext(ctx, `SELECT conversation_id FROM space_slack_links
			WHERE id=$1 AND space_id=$2 AND disabled_at IS NULL`, linkID, spaceID).Scan(&conversationID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceNotFound
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_slack_links SET disabled_at=NOW(),
			status='disabled',updated_at=NOW() WHERE id=$1 AND space_id=$2`, linkID, spaceID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE space_conversations SET integration_status='disconnected',
			updated_at=NOW() WHERE id=$1`, conversationID)
		return err
	})
}
