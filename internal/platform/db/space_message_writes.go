package db

import (
	"context"
	"database/sql"
	"errors"
)

func (db *Database) deleteSpaceMessage(ctx context.Context, userID, spaceID, conversationID, messageID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		permissions, err := messagePermissionsTx(ctx, tx, userID, spaceID, conversationID)
		if err != nil {
			return err
		}
		if !permissions.Has(PermissionMessagesRead) {
			return ErrSpaceForbidden
		}
		var sender, threadRootID string
		if err := tx.QueryRowContext(ctx, `SELECT sender_user_id,COALESCE(thread_root_id,'') FROM space_messages WHERE id=$1 AND space_id=$2 AND COALESCE(conversation_id,'')=$3 FOR UPDATE`, messageID, spaceID, conversationID).Scan(&sender, &threadRootID); errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		} else if err != nil {
			return err
		}
		if sender == userID && !permissions.Has(PermissionMessagesWrite) ||
			sender != userID && !permissions.Has(PermissionMessagesManage) {
			return ErrSpaceForbidden
		}
		replyIDs := []string{}
		if threadRootID == "" {
			if replyIDs, err = threadReplyIDsTx(ctx, tx, spaceID, messageID); err != nil {
				return err
			}
		}
		if err := cleanupSpaceMessagesTx(ctx, tx, spaceID, append([]string{messageID}, replyIDs...)); err != nil {
			return err
		}
		for _, replyID := range replyIDs {
			if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "message.deleted", replyID, map[string]any{
				"message_id": replyID, "conversation_id": conversationID, "thread_root_id": messageID,
			}); err != nil {
				return err
			}
		}
		payload := map[string]any{"message_id": messageID, "conversation_id": conversationID}
		if threadRootID != "" {
			payload["thread_root_id"] = threadRootID
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "message.deleted", messageID, payload)
		return err
	})
}

func threadReplyIDsTx(ctx context.Context, tx *sql.Tx, spaceID, rootID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM space_messages WHERE space_id=$1 AND thread_root_id=$2 FOR UPDATE`, spaceID, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
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
