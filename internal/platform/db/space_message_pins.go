package db

import (
	"context"
	"database/sql"
	"errors"
)

const maxPinnedMessages = 50

func requirePinRightTx(ctx context.Context, tx *sql.Tx, userID, spaceID, conversationID string) error {
	permissions, err := messagePermissionsTx(ctx, tx, userID, spaceID, conversationID)
	if err != nil {
		return err
	}
	if !permissions.Has(PermissionMessagesRead) {
		return ErrSpaceForbidden
	}
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM spaces WHERE id=$1`, spaceID).Scan(&kind); err != nil {
		return err
	}
	if kind == "direct" || permissions.Has(PermissionMessagesManage) {
		return nil
	}
	return ErrSpaceForbidden
}

func (db *Database) SetSpaceMessagePinned(ctx context.Context, userID, spaceID, conversationID, messageID string, pinned bool) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requirePinRightTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_messages WHERE id=$1 AND space_id=$2 AND COALESCE(conversation_id,'')=$3)`,
			messageID, spaceID, conversationID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrSpaceNotFound
		}
		if pinned {
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM space_message_pins p JOIN space_messages m ON m.id=p.message_id
				WHERE p.space_id=$1 AND COALESCE(m.conversation_id,'')=$2`, spaceID, conversationID).Scan(&count); err != nil {
				return err
			}
			if count >= maxPinnedMessages {
				return ErrSpaceLimit
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_message_pins(message_id,space_id,pinned_by) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, messageID, spaceID, userID); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(ctx, `DELETE FROM space_message_pins WHERE message_id=$1 AND space_id=$2`, messageID, spaceID); err != nil {
			return err
		}
		eventType := "message.unpinned"
		if pinned {
			eventType = "message.pinned"
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, eventType, messageID,
			map[string]any{"message_id": messageID, "conversation_id": conversationID})
		return err
	})
}

func (db *Database) SpacePinnedMessages(ctx context.Context, userID, spaceID, conversationID string) ([]SpaceMessage, error) {
	items := []SpaceMessage{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceChannelPermissionTx(ctx, tx, userID, spaceID, conversationID, PermissionMessagesRead); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceMessageColumns+` FROM space_message_pins p
			JOIN space_messages m ON m.id=p.message_id LEFT JOIN users u ON u.id=m.sender_user_id
			WHERE p.space_id=$1 AND COALESCE(m.conversation_id,'')=$2 ORDER BY p.pinned_at DESC LIMIT $3`, spaceID, conversationID, maxPinnedMessages)
		if err != nil {
			return err
		}
		for rows.Next() {
			var item SpaceMessage
			if err := scanSpaceMessage(rows, &item); err != nil {
				rows.Close()
				return err
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for index := range items {
			if err := loadSpaceMessageReferencesTx(ctx, tx, &items[index], userID); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return items, err
}
