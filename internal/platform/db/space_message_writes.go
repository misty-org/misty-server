package db

import (
	"context"
	"database/sql"
	"errors"
)

func (db *Database) deleteSpaceMessage(ctx context.Context, userID, spaceID, conversationID, messageID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceMessageWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		if conversationID != "" {
			if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID); err != nil {
				return err
			}
		}
		role, err := requireSpaceMemberTx(ctx, tx, spaceID, userID)
		if err != nil {
			return err
		}
		var sender string
		if err := tx.QueryRowContext(ctx, `SELECT sender_user_id FROM space_messages WHERE id=$1 AND space_id=$2 AND COALESCE(conversation_id,'')=$3 FOR UPDATE`, messageID, spaceID, conversationID).Scan(&sender); errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		} else if err != nil {
			return err
		}
		if sender != userID && role != "owner" {
			return ErrSpaceForbidden
		}
		if err := cleanupSpaceMessagesTx(ctx, tx, spaceID, []string{messageID}); err != nil {
			return err
		}
		// Recorded after the cleanup, which removes the message's own events: everyone who could
		// see the message takes it off their screen. Only ids travel, never what it said.
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "message.deleted", messageID,
			map[string]any{"message_id": messageID, "conversation_id": conversationID})
		return err
	})
}

func (db *Database) SpaceNodes(ctx context.Context, userID, spaceID string) ([]SpaceNode, error) {
	items := []SpaceNode{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,space_id,COALESCE(parent_id,''),kind,display_name,uploader_user_id,mime_type,size_bytes,stale,metadata,created_at,updated_at
			FROM space_nodes WHERE space_id=$1 AND (visibility='public' OR uploader_user_id=$2) ORDER BY parent_id NULLS FIRST,kind,display_name`, spaceID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceNode
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.ParentID, &item.Kind, &item.DisplayName, &item.UploaderUserID, &item.MIMEType, &item.SizeBytes, &item.Stale, &item.Metadata, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) SpaceNodeSecret(ctx context.Context, userID, spaceID, nodeID string) (*SpaceNode, error) {
	out := &SpaceNode{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT id,space_id,COALESCE(parent_id,''),kind,display_name,uploader_user_id,mime_type,size_bytes,stale,metadata,created_at,updated_at,target_ciphertext,target_nonce,COALESCE(target_key_version,0)
			FROM space_nodes WHERE id=$1 AND space_id=$2 AND (visibility='public' OR uploader_user_id=$3)`, nodeID, spaceID, userID).Scan(&out.ID, &out.SpaceID, &out.ParentID, &out.Kind, &out.DisplayName, &out.UploaderUserID, &out.MIMEType, &out.SizeBytes, &out.Stale, &out.Metadata, &out.CreatedAt, &out.UpdatedAt, &out.TargetCipher, &out.TargetNonce, &out.KeyVersion)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	return out, err
}
