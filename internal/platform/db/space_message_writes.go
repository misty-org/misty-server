package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
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
		return cleanupSpaceMessagesTx(ctx, tx, spaceID, []string{messageID})
	})
}

func (db *Database) UpsertSpaceNode(ctx context.Context, userID string, node SpaceNode) (*SpaceNode, error) {
	node.DisplayName = strings.TrimSpace(node.DisplayName)
	if node.ID == "" {
		node.ID = "node_" + uuid.NewString()
	}
	if len([]rune(node.DisplayName)) < 1 || len([]rune(node.DisplayName)) > 255 || (node.Kind != "folder" && node.Kind != "link") {
		return nil, ErrSpaceInvalid
	}
	if node.Kind == "link" && (len(node.TargetCipher) == 0 || len(node.TargetNonce) == 0) {
		return nil, ErrSpaceInvalid
	}
	node.UploaderUserID = userID
	if len(node.Metadata) == 0 {
		node.Metadata = json.RawMessage(`{}`)
	}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceMessageWriteTx(ctx, tx, userID, node.SpaceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "spaces:nodes:"+node.SpaceID); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM space_nodes WHERE space_id=$1`, node.SpaceID).Scan(&count); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_nodes WHERE id=$1 AND space_id=$2)`, node.ID, node.SpaceID).Scan(&exists); err != nil {
			return err
		}
		if !exists && count >= MaxSpaceNodes {
			return ErrSpaceNodeLimit
		}
		if node.ParentID != "" {
			if node.ParentID == node.ID {
				return ErrSpaceInvalid
			}
			var parentOK bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_nodes WHERE id=$1 AND space_id=$2 AND kind='folder')`, node.ParentID, node.SpaceID).Scan(&parentOK); err != nil || !parentOK {
				return ErrSpaceInvalid
			}
			if exists && node.Kind == "folder" {
				var cycle bool
				if err := tx.QueryRowContext(ctx, `WITH RECURSIVE descendants AS (
					SELECT id FROM space_nodes WHERE parent_id=$1 AND space_id=$2
					UNION ALL SELECT child.id FROM space_nodes child JOIN descendants parent ON child.parent_id=parent.id WHERE child.space_id=$2
				) SELECT EXISTS(SELECT 1 FROM descendants WHERE id=$3)`, node.ID, node.SpaceID, node.ParentID).Scan(&cycle); err != nil {
					return err
				}
				if cycle {
					return ErrSpaceInvalid
				}
			}
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_nodes(id,space_id,parent_id,kind,display_name,uploader_user_id,target_ciphertext,target_nonce,target_key_version,mime_type,size_bytes,metadata)
			VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,NULLIF($9,0),$10,$11,$12)
			ON CONFLICT(id) DO UPDATE SET parent_id=excluded.parent_id,display_name=excluded.display_name,mime_type=excluded.mime_type,size_bytes=excluded.size_bytes,metadata=excluded.metadata,updated_at=NOW()
			WHERE space_nodes.space_id=excluded.space_id
			RETURNING created_at,updated_at`, node.ID, node.SpaceID, node.ParentID, node.Kind, node.DisplayName, userID, nullableBytes(node.TargetCipher), nullableBytes(node.TargetNonce), node.KeyVersion, node.MIMEType, node.SizeBytes, node.Metadata).Scan(&node.CreatedAt, &node.UpdatedAt); err != nil {
			return err
		}
		eventType := "node.updated"
		if !exists {
			eventType = "node.created"
		}
		_, err := recordSpaceEventTx(ctx, tx, node.SpaceID, userID, eventType, node.ID, node)
		return err
	})
	return &node, err
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
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
