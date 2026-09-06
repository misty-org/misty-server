package db

import (
	"context"
	"database/sql"
	"errors"
)

// UpdateSpaceConversation renames a conversation and/or changes its member
// list. Only the conversation's creator may do this — the same restriction
// the space_conversations_creator_write / space_conversation_members_creator_write
// RLS policies enforce at the database layer for non-superuser roles. This
// checks explicitly too so the behavior is identical (a clear
// ErrSpaceForbidden, not a silently-ignored zero-row update) regardless of
// which role is connected, including in tests that run as a superuser and
// would otherwise bypass RLS entirely.
func (db *Database) UpdateSpaceConversation(ctx context.Context, userID, spaceID, conversationID, title string, participantRefs []SpaceActorRef) (*SpaceConversation, error) {
	title, err := normalizeConversationTitle(title)
	if err != nil {
		return nil, err
	}
	participants := normalizeSpaceActorRefs(append(participantRefs, SpaceActorRef{Kind: "person", UserID: userID}))
	if len(participants) < 2 {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceConversation{ID: conversationID, SpaceID: spaceID, Title: title, Kind: "standard"}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
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
		if createdByUserID != userID {
			return ErrSpaceForbidden
		}
		if conversationKind != "standard" {
			return ErrSpaceForbidden
		}
		out.CreatedByUserID = createdByUserID
		if err := validateSpaceActorRefsTx(ctx, tx, userID, spaceID, participants); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE space_conversations SET title=$1, updated_at=NOW() WHERE id=$2 AND space_id=$3`,
			title, conversationID, spaceID,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_conversation_members WHERE conversation_id=$1`, conversationID); err != nil {
			return err
		}
		for _, participant := range participants {
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_conversation_members(conversation_id,user_id,agent_id,actor_kind) VALUES($1,NULLIF($2,''),NULLIF($3,''),$4)`, conversationID, participant.UserID, participant.AgentID, participant.Kind); err != nil {
				return err
			}
		}
		if err := tx.QueryRowContext(ctx,
			`SELECT created_at, updated_at FROM space_conversations WHERE id=$1`, conversationID,
		).Scan(&out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "conversation.updated", conversationID,
			map[string]any{"conversation_id": conversationID, "title": title, "participants": participants},
		); err != nil {
			return err
		}
		return loadSpaceConversationParticipantsTx(ctx, tx, out)
	})
	return out, err
}

func (db *Database) DeleteDisconnectedDiscordConversation(
	ctx context.Context,
	userID, spaceID, conversationID string,
) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceOwnerTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		participantUserIDs, err := humanMemberIDsForConversationTx(ctx, tx, conversationID)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM space_conversations
			WHERE id=$1 AND space_id=$2 AND origin='discord' AND integration_status='disconnected'`,
			conversationID, spaceID)
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

func (db *Database) SpaceConversationMessages(ctx context.Context, userID, spaceID, conversationID string, before int64, limit int) ([]SpaceMessage, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	items := []SpaceMessage{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return err
		}
		if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceMessageColumns+` FROM space_messages m
			LEFT JOIN users u ON u.id=m.sender_user_id LEFT JOIN space_agents a ON a.id=m.sender_agent_id
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
