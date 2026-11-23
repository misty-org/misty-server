package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

func loadSpaceMessageReferencesTx(ctx context.Context, tx *sql.Tx, message *SpaceMessage, userID string) error {
	message.Attachments = []MessageAttachment{}
	message.Reactions = []SpaceMessageReaction{}
	rows, err := tx.QueryContext(ctx, `SELECT id,space_id,COALESCE(message_id,''),file_id,upload_id,uploader_user_id,display_name,lifecycle_state,created_at,deleted_at,recover_until FROM space_message_attachments WHERE message_id=$1 AND lifecycle_state='ready' ORDER BY created_at`, message.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var attachment MessageAttachment
		if err := scanMessageAttachment(rows, &attachment); err != nil {
			return err
		}
		message.Attachments = append(message.Attachments, attachment)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	reactionRows, err := tx.QueryContext(ctx, `SELECT emoji,count(*),bool_or(user_id=$2)
		FROM space_message_reactions WHERE message_id=$1 GROUP BY emoji ORDER BY min(created_at),emoji`, message.ID, userID)
	if err != nil {
		return err
	}
	defer reactionRows.Close()
	for reactionRows.Next() {
		var reaction SpaceMessageReaction
		if err := reactionRows.Scan(&reaction.Emoji, &reaction.Count, &reaction.ReactedByMe); err != nil {
			return err
		}
		message.Reactions = append(message.Reactions, reaction)
	}
	return reactionRows.Err()
}

func (db *Database) UpdateSpaceMessage(ctx context.Context, userID, spaceID, messageID string, content []MessageSpan, fileNodeIDs []string) (*SpaceMessage, error) {
	return db.updateSpaceMessage(ctx, userID, spaceID, "", messageID, content, fileNodeIDs)
}

func (db *Database) UpdateSpaceConversationMessage(ctx context.Context, userID, spaceID, conversationID, messageID string, content []MessageSpan, fileNodeIDs []string) (*SpaceMessage, error) {
	return db.updateSpaceMessage(ctx, userID, spaceID, conversationID, messageID, content, fileNodeIDs)
}

func (db *Database) updateSpaceMessage(ctx context.Context, userID, spaceID, conversationID, messageID string, content []MessageSpan, fileNodeIDs []string) (*SpaceMessage, error) {
	if err := ValidateMessage(content, fileNodeIDs); err != nil {
		return nil, err
	}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceChannelWriteTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return err
		}
		var sender, threadRootID string
		var previous []byte
		if err := tx.QueryRowContext(ctx, `SELECT sender_user_id,content,COALESCE(thread_root_id,'') FROM space_messages WHERE id=$1 AND space_id=$2 AND COALESCE(conversation_id,'')=$3 FOR NO KEY UPDATE`, messageID, spaceID, conversationID).Scan(&sender, &previous, &threadRootID); errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		} else if err != nil {
			return err
		}
		if sender != userID {
			return ErrSpaceForbidden
		}
		var previousSpans []MessageSpan
		_ = json.Unmarshal(previous, &previousSpans)
		if err := validateRoleMentionsTx(ctx, tx, userID, spaceID, content, previousSpans); err != nil {
			return err
		}
		mentionedAgents := map[string]bool{}
		for _, span := range previousSpans {
			if span.AgentID != "" {
				mentionedAgents[span.AgentID] = true
			}
		}
		for _, span := range content {
			if span.AgentID == "" || mentionedAgents[span.AgentID] {
				continue
			}
			if err := requireConversationAgentMentionTx(ctx, tx, userID, spaceID, conversationID, span.AgentID); err != nil {
				return err
			}
			mentionedAgents[span.AgentID] = true
		}
		for _, nodeID := range fileNodeIDs {
			var ok bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_nodes WHERE id=$1 AND space_id=$2 AND kind='link')`, nodeID, spaceID).Scan(&ok); err != nil || !ok {
				return ErrSpaceInvalid
			}
		}
		raw, _ := json.Marshal(content)
		var editedAt sql.NullTime
		if err := tx.QueryRowContext(ctx, `UPDATE space_messages SET content=$1,file_node_ids=$2,edited_at=NOW() WHERE id=$3 RETURNING edited_at`, raw, pqStringArray(fileNodeIDs), messageID).Scan(&editedAt); err != nil {
			return err
		}
		for _, span := range content {
			if span.UserID != "" && span.UserID != sender {
				memberQuery := `SELECT EXISTS(SELECT 1 FROM space_members WHERE space_id=$1 AND user_id=$2)`
				memberArgs := []any{spaceID, span.UserID}
				if conversationID != "" {
					memberQuery = `SELECT EXISTS(SELECT 1 FROM space_conversation_members cm JOIN space_conversations c ON c.id=cm.conversation_id WHERE c.space_id=$1 AND cm.user_id=$2 AND cm.conversation_id=$3)`
					memberArgs = append(memberArgs, conversationID)
				}
				var allowed bool
				if err := tx.QueryRowContext(ctx, memberQuery, memberArgs...).Scan(&allowed); err != nil {
					return err
				}
				if !allowed {
					return ErrSpaceInvalid
				}
			}
		}
		var senderName string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT username FROM users WHERE id=$1),'')`, sender).Scan(&senderName); err != nil {
			return err
		}
		payload := map[string]any{
			"conversation_id": conversationID, "message_id": messageID, "content": content,
			"file_node_ids": fileNodeIDs, "sender_user_id": sender, "sender_name": senderName,
		}
		if editedAt.Valid {
			payload["edited_at"] = editedAt.Time
		}
		if threadRootID != "" {
			payload["thread_root_id"] = threadRootID
		}
		_, eventErr := recordSpaceEventTx(ctx, tx, spaceID, userID, "message.updated", messageID, payload)
		return eventErr
	})
	if err != nil {
		return nil, err
	}
	out := &SpaceMessage{}
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceChannelPermissionTx(ctx, tx, userID, spaceID, conversationID, PermissionMessagesRead); err != nil {
			return err
		}
		if err := scanSpaceMessage(tx.QueryRowContext(ctx, `SELECT `+spaceMessageColumns+` FROM space_messages m LEFT JOIN users u ON u.id=m.sender_user_id WHERE m.id=$1 AND m.space_id=$2 AND COALESCE(m.conversation_id,'')=$3`, messageID, spaceID, conversationID), out); err != nil {
			return err
		}
		if err := loadSpaceMessageReferencesTx(ctx, tx, out, userID); err != nil {
			return err
		}
		return loadSingleThreadSummaryTx(ctx, tx, out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func loadSingleThreadSummaryTx(ctx context.Context, tx *sql.Tx, message *SpaceMessage) error {
	if message.ThreadRootID != "" {
		return nil
	}
	items := []SpaceMessage{*message}
	if err := loadThreadSummariesTx(ctx, tx, items); err != nil {
		return err
	}
	message.ReplyCount, message.LastReplyAt = items[0].ReplyCount, items[0].LastReplyAt
	return nil
}

func spaceMessageReactionCountsTx(ctx context.Context, tx *sql.Tx, messageID string) ([]map[string]any, error) {
	rows, err := tx.QueryContext(ctx, `SELECT emoji,count(*) FROM space_message_reactions WHERE message_id=$1 GROUP BY emoji ORDER BY min(created_at),emoji`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var emoji string
		var count int
		if err := rows.Scan(&emoji, &count); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"emoji": emoji, "count": count})
	}
	return out, rows.Err()
}

func (db *Database) AddSpaceMessageReaction(ctx context.Context, userID, spaceID, messageID, emoji string) (*SpaceMessage, error) {
	return db.setSpaceMessageReaction(ctx, userID, spaceID, "", messageID, emoji, true)
}

func (db *Database) RemoveSpaceMessageReaction(ctx context.Context, userID, spaceID, messageID, emoji string) (*SpaceMessage, error) {
	return db.setSpaceMessageReaction(ctx, userID, spaceID, "", messageID, emoji, false)
}

func (db *Database) AddSpaceConversationMessageReaction(ctx context.Context, userID, spaceID, conversationID, messageID, emoji string) (*SpaceMessage, error) {
	return db.setSpaceMessageReaction(ctx, userID, spaceID, conversationID, messageID, emoji, true)
}

func (db *Database) RemoveSpaceConversationMessageReaction(ctx context.Context, userID, spaceID, conversationID, messageID, emoji string) (*SpaceMessage, error) {
	return db.setSpaceMessageReaction(ctx, userID, spaceID, conversationID, messageID, emoji, false)
}

func (db *Database) setSpaceMessageReaction(ctx context.Context, userID, spaceID, conversationID, messageID, emoji string, reacted bool) (*SpaceMessage, error) {
	emoji, err := normalizeMessageReactionEmoji(emoji)
	if err != nil {
		return nil, err
	}
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceChannelWriteTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return err
		}
		var locked string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM space_messages WHERE id=$1 AND space_id=$2 AND COALESCE(conversation_id,'')=$3 FOR NO KEY UPDATE`, messageID, spaceID, conversationID).Scan(&locked); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceNotFound
			}
			return err
		}
		if reacted {
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_message_reactions(message_id,space_id,user_id,emoji)
				VALUES($1,$2,$3,$4) ON CONFLICT(message_id,user_id,emoji) DO NOTHING`, messageID, spaceID, userID, emoji); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(ctx, `DELETE FROM space_message_reactions WHERE message_id=$1 AND user_id=$2 AND emoji=$3`, messageID, userID, emoji); err != nil {
			return err
		}
		eventType := "message.reaction_removed"
		if reacted {
			eventType = "message.reaction_added"
		}
		counts, err := spaceMessageReactionCountsTx(ctx, tx, messageID)
		if err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, eventType, messageID, map[string]any{
			"conversation_id": conversationID, "emoji": emoji, "message_id": messageID, "reactions": counts,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return db.spaceMessageByID(ctx, userID, spaceID, conversationID, messageID)
}

func (db *Database) spaceMessageByID(ctx context.Context, userID, spaceID, conversationID, messageID string) (*SpaceMessage, error) {
	out := &SpaceMessage{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceChannelPermissionTx(ctx, tx, userID, spaceID, conversationID, PermissionMessagesRead); err != nil {
			return err
		}
		if err := scanSpaceMessage(tx.QueryRowContext(ctx, `SELECT `+spaceMessageColumns+` FROM space_messages m LEFT JOIN users u ON u.id=m.sender_user_id WHERE m.id=$1 AND m.space_id=$2 AND COALESCE(m.conversation_id,'')=$3`, messageID, spaceID, conversationID), out); err != nil {
			return err
		}
		if err := loadSpaceMessageReferencesTx(ctx, tx, out, userID); err != nil {
			return err
		}
		return loadSingleThreadSummaryTx(ctx, tx, out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func normalizeMessageReactionEmoji(emoji string) (string, error) {
	emoji = strings.TrimSpace(emoji)
	if emoji == "" || !utf8.ValidString(emoji) || len([]byte(emoji)) > 32 || utf8.RuneCountInString(emoji) > 8 {
		return "", ErrSpaceInvalid
	}
	if strings.ContainsAny(emoji, " \t\r\n") {
		return "", ErrSpaceInvalid
	}
	return emoji, nil
}

func (db *Database) DeleteSpaceMessage(ctx context.Context, userID, spaceID, messageID string) error {
	return db.deleteSpaceMessage(ctx, userID, spaceID, "", messageID)
}

func (db *Database) DeleteSpaceConversationMessage(ctx context.Context, userID, spaceID, conversationID, messageID string) error {
	return db.deleteSpaceMessage(ctx, userID, spaceID, conversationID, messageID)
}
