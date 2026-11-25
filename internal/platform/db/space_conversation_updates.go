package db

import (
	"context"
	"database/sql"
	"errors"
)

func (db *Database) UpdateSpaceConversation(ctx context.Context, userID, spaceID, conversationID, title string) (*SpaceConversation, error) {
	title, err := normalizeConversationTitle(title)
	if err != nil {
		return nil, err
	}
	out := &SpaceConversation{ID: conversationID, SpaceID: spaceID, Title: title, Kind: "standard", VisibleToSpace: true}
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := memberAccessTx(ctx, tx, spaceID, userID)
		if err != nil {
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
		permissions, _, err := channelPermissionsForTx(ctx, tx, access, userID, spaceID, conversationID)
		if err != nil {
			return err
		}
		if !permissions.Has(PermissionMessagesRead) {
			return ErrSpaceNotFound
		}
		if conversationKind != "standard" || !permissions.Has(PermissionChannelsManage) {
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
			`SELECT voice, category_id, position, created_at, updated_at FROM space_conversations WHERE id=$1`, conversationID,
		).Scan(&out.Voice, &out.CategoryID, &out.Position, &out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "conversation.updated", conversationID,
			map[string]any{"conversation_id": conversationID, "title": title},
		); err != nil {
			return err
		}
		if err := loadSpaceConversationParticipantsTx(ctx, tx, out); err != nil {
			return err
		}
		return fillConversationAccessTx(ctx, tx, access, out)
	})
	return out, err
}

func (db *Database) SpaceConversationMessages(ctx context.Context, userID, spaceID, conversationID string, before int64, limit int) ([]SpaceMessage, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	items := []SpaceMessage{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceMessageColumns+` FROM space_messages m
			LEFT JOIN users u ON u.id=m.sender_user_id
			WHERE m.space_id=$1 AND m.conversation_id=$2 AND m.thread_root_id IS NULL AND ($3=0 OR m.seq<$3) ORDER BY m.seq DESC LIMIT $4`, spaceID, conversationID, before, limit)
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
		return loadThreadSummariesTx(ctx, tx, items)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return items, err
}
