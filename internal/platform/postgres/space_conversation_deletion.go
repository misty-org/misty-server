package db

import (
	"context"
	"database/sql"
	"errors"
)

func cleanupSpaceMessagesTx(ctx context.Context, tx *sql.Tx, spaceID string, messageIDs []string) error {
	messageIDs = uniqueSpaceIDs(messageIDs)
	if len(messageIDs) == 0 {
		return nil
	}

	rows, err := tx.QueryContext(ctx, `SELECT a.id,a.file_id,a.upload_id,COALESCE(a.promoted_item_id,''),
		COALESCE(c.logical_bytes,0)
		FROM space_message_attachments a
		LEFT JOIN space_storage_contributions c ON c.space_id=a.space_id AND c.source_kind='attachment'
			AND c.source_id=a.id AND c.state IN ('active','recovery')
		WHERE a.space_id=$1 AND a.message_id=ANY($2::text[]) FOR UPDATE OF a`, spaceID, pqStringArray(messageIDs))
	if err != nil {
		return err
	}
	type attachmentCleanup struct {
		id, fileID, uploadID, promotedItemID string
		bytes                                int64
	}
	items := []attachmentCleanup{}
	for rows.Next() {
		var item attachmentCleanup
		if err := rows.Scan(&item.id, &item.fileID, &item.uploadID, &item.promotedItemID, &item.bytes); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	var released int64
	for _, item := range items {
		if item.promotedItemID != "" {
			// Promotion deliberately transfers retention to the Library.
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_item_aliases WHERE space_id=$1 AND target_kind='attachment' AND target_id=$2`, spaceID, item.id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_storage_contributions SET state='released',released_at=NOW(),updated_at=NOW()
			WHERE space_id=$1 AND source_kind='attachment' AND source_id=$2 AND state IN ('active','recovery')`, spaceID, item.id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_message_attachments SET lifecycle_state='deleted',deleted_at=NOW(),recover_until=NULL WHERE id=$1`, item.id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_library_uploads SET state='deleted',version=version+1,updated_at=NOW() WHERE id=$1`, item.uploadID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE library_files f SET lifecycle_state='deleted',deleted_at=NOW(),version=version+1,updated_at=NOW()
			WHERE f.id=$1
			  AND NOT EXISTS(SELECT 1 FROM space_library_items i WHERE i.file_id=f.id AND i.lifecycle_state IN ('ready','recovery'))
			  AND NOT EXISTS(SELECT 1 FROM space_message_attachments a WHERE a.file_id=f.id AND a.id<>$2 AND a.lifecycle_state IN ('ready','recovery'))
			  AND NOT EXISTS(SELECT 1 FROM space_note_assets n WHERE n.file_id=f.id AND n.lifecycle_state IN ('ready','recovery'))
			  AND NOT EXISTS(SELECT 1 FROM space_drawing_assets d WHERE d.file_id=f.id AND d.lifecycle_state IN ('ready','recovery'))`, item.fileID, item.id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE library_blobs b SET lifecycle_state='deleted',deleted_at=NOW(),version=version+1,updated_at=NOW()
			WHERE b.id=(SELECT blob_id FROM library_files WHERE id=$1)
			  AND NOT EXISTS(SELECT 1 FROM library_files f WHERE f.blob_id=b.id AND f.lifecycle_state<>'deleted')`, item.fileID); err != nil {
			return err
		}
		released += item.bytes
	}
	if released > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET used_bytes=GREATEST(0,used_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, released, spaceID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM space_inbox_items WHERE message_id=ANY($1::text[])`, pqStringArray(messageIDs)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM space_events WHERE entity_id=ANY($1::text[])`, pqStringArray(messageIDs)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM space_messages WHERE space_id=$1 AND id=ANY($2::text[])`, spaceID, pqStringArray(messageIDs))
	return err
}

func messageIDsForConversationTx(ctx context.Context, tx *sql.Tx, spaceID, conversationID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM space_messages WHERE space_id=$1 AND conversation_id=$2 FOR UPDATE`, spaceID, conversationID)
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

func humanMemberIDsForConversationTx(ctx context.Context, tx *sql.Tx, conversationID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT user_id FROM space_conversation_members
		WHERE conversation_id=$1 AND actor_kind='person' AND user_id IS NOT NULL`, conversationID)
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

func cleanupSpaceConversationRunsTx(ctx context.Context, tx *sql.Tx, spaceID, conversationID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM space_events e USING space_runs r
		WHERE e.entity_id=r.id AND r.space_id=$1
		AND (r.scope_conversation_id=$2 OR r.source_conversation_id=$2)`, spaceID, conversationID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM space_runs
		WHERE space_id=$1 AND (scope_conversation_id=$2 OR source_conversation_id=$2)`, spaceID, conversationID)
	return err
}

// DeleteOrClearSpaceConversation permanently closes a conversation and removes
// its history. Direct Agent conversations are recreated on demand, so keeping
// an empty canonical row here would make a deleted chat remain in the sidebar.
func (db *Database) DeleteOrClearSpaceConversation(ctx context.Context, userID, spaceID, conversationID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return err
		}
		var kind, creator, origin, integrationStatus string
		if err := tx.QueryRowContext(ctx, `SELECT kind,created_by_user_id,origin,integration_status
			FROM space_conversations WHERE id=$1 AND space_id=$2 FOR UPDATE`, conversationID, spaceID).
			Scan(&kind, &creator, &origin, &integrationStatus); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceNotFound
			}
			return err
		}
		role, err := requireSpaceMemberTx(ctx, tx, spaceID, userID)
		if err != nil {
			return err
		}
		if kind != "direct" && (kind != "standard" || (creator != userID && role != "owner")) {
			return ErrSpaceForbidden
		}
		if origin == "discord" && integrationStatus != "disconnected" {
			return ErrSpaceForbidden
		}
		participantUserIDs, err := humanMemberIDsForConversationTx(ctx, tx, conversationID)
		if err != nil {
			return err
		}
		if err := cleanupSpaceConversationRunsTx(ctx, tx, spaceID, conversationID); err != nil {
			return err
		}
		ids, err := messageIDsForConversationTx(ctx, tx, spaceID, conversationID)
		if err != nil {
			return err
		}
		if err := cleanupSpaceMessagesTx(ctx, tx, spaceID, ids); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM space_conversations WHERE id=$1 AND space_id=$2`, conversationID, spaceID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrSpaceNotFound
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "conversation.deleted",
			conversationID, map[string]any{
				"conversation_id": conversationID, "participant_user_ids": participantUserIDs,
			})
		return err
	})
}

func (db *Database) ClearEveryoneConversation(ctx context.Context, userID, spaceID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceOwnerTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM space_messages WHERE space_id=$1 AND conversation_id IS NULL FOR UPDATE`, spaceID)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		return cleanupSpaceMessagesTx(ctx, tx, spaceID, ids)
	})
}
