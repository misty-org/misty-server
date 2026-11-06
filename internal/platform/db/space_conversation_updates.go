package db

import (
	"context"
	"database/sql"
	"errors"
)

// UpdateSpaceConversation renames a group chat. Everyone in the Space is in a
// group chat, so its members don't change, and a DM can't be changed at all. Only
// the Space's owner may do this. This checks explicitly as well as through RLS so
// the behavior is identical (a clear ErrSpaceForbidden, not a silently-ignored
// zero-row update) regardless of which role is connected, including in tests that
// run as a superuser and would otherwise bypass RLS entirely.
func (db *Database) UpdateSpaceConversation(ctx context.Context, userID, spaceID, conversationID, title string) (*SpaceConversation, error) {
	title, err := normalizeConversationTitle(title)
	if err != nil {
		return nil, err
	}
	out := &SpaceConversation{ID: conversationID, SpaceID: spaceID, Title: title, Kind: "standard", VisibleToSpace: true}
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceMessageWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		var createdByUserID, conversationKind string
		if err := tx.QueryRowContext(ctx,
			`SELECT created_by_user_id,kind FROM space_conversations WHERE id=$1 AND space_id=$2 FOR UPDATE`,
			conversationID, spaceID,
		).Scan(&createdByUserID, &conversationKind); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceNotFound
			}
			return err
		}
		// Only the Space's owner renames or re-seats a conversation.
		role, err := requireSpaceMemberTx(ctx, tx, spaceID, userID)
		if err != nil {
			return err
		}
		if role != "owner" {
			return ErrSpaceForbidden
		}
		if conversationKind != "standard" {
			return ErrSpaceForbidden
		}
		out.CreatedByUserID = createdByUserID
		if _, err := tx.ExecContext(ctx,
			`UPDATE space_conversations SET title=$1, updated_at=NOW() WHERE id=$2 AND space_id=$3`,
			title, conversationID, spaceID,
		); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx,
			`SELECT created_at, updated_at FROM space_conversations WHERE id=$1`, conversationID,
		).Scan(&out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "conversation.updated", conversationID,
			map[string]any{"conversation_id": conversationID, "title": title},
		); err != nil {
			return err
		}
		return loadSpaceConversationParticipantsTx(ctx, tx, out)
	})
	return out, err
}

func (db *Database) SpaceConversationMessages(ctx context.Context, userID, spaceID, conversationID string, before int64, limit int) ([]SpaceMessage, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	items := []SpaceMessage{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return err
		}
		if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceMessageColumns+` FROM space_messages m
			LEFT JOIN users u ON u.id=m.sender_user_id LEFT JOIN misty_ask_identities a ON a.id=m.sender_agent_id
			WHERE m.space_id=$1 AND m.conversation_id=$2 AND ($3=0 OR m.seq<$3) ORDER BY m.seq DESC LIMIT $4`, spaceID, conversationID, before, limit)
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
		if err := rows.Close(); err != nil {
			return err
		}
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
