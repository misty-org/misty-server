package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

func (db *Database) createSpaceMessageWithReferences(ctx context.Context, userID, spaceID, conversationID string, content []MessageSpan, fileNodeIDs, attachmentIDs, libraryItemIDs []string, replyToMessageID, clientNonce string) (*SpaceMessage, []string, error) {
	if err := validateMessageWithReferences(content, len(fileNodeIDs)+len(attachmentIDs)+len(libraryItemIDs)); err != nil {
		return nil, nil, err
	}
	clientNonce = strings.TrimSpace(clientNonce)
	if len(clientNonce) > 128 {
		return nil, nil, ErrSpaceInvalid
	}
	out := &SpaceMessage{ID: "msg_" + uuid.NewString(), ClientNonce: clientNonce, SpaceID: spaceID, ConversationID: conversationID, SenderUserID: userID, SenderKind: "person", Content: content, FileNodeIDs: fileNodeIDs, LibraryItemIDs: uniqueSpaceIDs(libraryItemIDs), Attachments: []MessageAttachment{}, Reactions: []SpaceMessageReaction{}, ReplyToMessageID: replyToMessageID}
	attachmentIDs = uniqueSpaceIDs(attachmentIDs)
	agentMentions := []string{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceMessageWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		if conversationID == "" {
			if err := requireStandardSpaceTx(ctx, tx, spaceID); err != nil {
				return err
			}
		} else {
			if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID); err != nil {
				return err
			}
		}
		if len(out.LibraryItemIDs) > 0 {
			if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
				return err
			}
		}
		for _, nodeID := range fileNodeIDs {
			var ok bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_nodes WHERE id=$1 AND space_id=$2 AND kind='link')`, nodeID, spaceID).Scan(&ok); err != nil || !ok {
				return ErrSpaceInvalid
			}
		}
		if replyToMessageID != "" {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_messages WHERE id=$1 AND space_id=$2 AND COALESCE(conversation_id,'')=$3)`, replyToMessageID, spaceID, conversationID).Scan(&exists); err != nil || !exists {
				return ErrSpaceInvalid
			}
		}
		for _, itemID := range out.LibraryItemIDs {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_library_items WHERE id=$1 AND space_id=$2 AND lifecycle_state='ready')`, itemID, spaceID).Scan(&exists); err != nil || !exists {
				return ErrSpaceInvalid
			}
		}
		for _, attachmentID := range attachmentIDs {
			var attachment MessageAttachment
			if err := scanMessageAttachment(tx.QueryRowContext(ctx, `SELECT a.id,a.space_id,COALESCE(a.message_id,''),a.file_id,a.upload_id,a.uploader_user_id,a.display_name,COALESCE(a.promoted_item_id,''),a.lifecycle_state,a.created_at,a.deleted_at,a.recover_until
				FROM space_message_attachments a JOIN space_library_uploads u ON u.id=a.upload_id
				WHERE a.id=$1 AND a.space_id=$2 AND a.uploader_user_id=$3 AND a.message_id IS NULL AND a.lifecycle_state='ready'
				  AND (u.conversation_id IS NULL OR u.conversation_id=NULLIF($4,''))
				FOR UPDATE OF a`, attachmentID, spaceID, userID, conversationID), &attachment); err != nil {
				return ErrSpaceInvalid
			}
			out.Attachments = append(out.Attachments, attachment)
		}
		mentionUsers := map[string]bool{}
		for _, span := range content {
			if span.UserID != "" {
				var ok bool
				query := `SELECT EXISTS(SELECT 1 FROM space_members WHERE space_id=$1 AND user_id=$2)`
				args := []any{spaceID, span.UserID}
				if conversationID != "" {
					query = `SELECT EXISTS(SELECT 1 FROM space_conversation_members cm JOIN space_conversations c ON c.id=cm.conversation_id WHERE c.space_id=$1 AND cm.user_id=$2 AND cm.conversation_id=$3)`
					args = append(args, conversationID)
				}
				if err := tx.QueryRowContext(ctx, query, args...).Scan(&ok); err != nil || !ok {
					return ErrSpaceInvalid
				}
				mentionUsers[span.UserID] = true
			}
			if span.AgentID != "" {
				membership, personalErr := activePersonalAgentMembershipTx(ctx, tx, userID, spaceID, span.AgentID)
				if personalErr != nil || !agentRolePermission(membership, PermissionMessagesWrite) {
					return ErrSpaceInvalid
				}
				if conversationID != "" {
					var participating bool
					if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_conversation_members
						WHERE conversation_id=$1 AND actor_kind='agent' AND agent_id=$2)`, conversationID, span.AgentID).Scan(&participating); err != nil || !participating {
						return ErrSpaceInvalid
					}
				}
				agentMentions = append(agentMentions, span.AgentID)
			}
		}
		raw, _ := json.Marshal(content)
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_messages(id,space_id,conversation_id,sender_user_id,content,file_node_ids,reply_to_message_id,expires_at)
			VALUES($1,$2,NULLIF($3,''),$4,$5,$6,NULLIF($7,''),NULL) RETURNING seq,created_at`, out.ID, spaceID, conversationID, userID, raw, pqStringArray(fileNodeIDs), replyToMessageID).Scan(&out.Seq, &out.CreatedAt); err != nil {
			return err
		}
		if conversationID != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE space_conversations SET updated_at=NOW() WHERE id=$1`, conversationID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_conversation_reads(conversation_id,user_id,read_message_seq)
				VALUES($1,$2,$3) ON CONFLICT(conversation_id,user_id) DO UPDATE
				SET read_message_seq=GREATEST(space_conversation_reads.read_message_seq,excluded.read_message_seq),updated_at=NOW()`, conversationID, userID, out.Seq); err != nil {
				return err
			}
		}
		for _, attachmentID := range attachmentIDs {
			if _, err := tx.ExecContext(ctx, `UPDATE space_message_attachments SET message_id=$1 WHERE id=$2`, out.ID, attachmentID); err != nil {
				return err
			}
		}
		for _, itemID := range out.LibraryItemIDs {
			var referenceAllowed bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_library_items WHERE id=$1 AND space_id=$2 AND lifecycle_state='ready' AND hidden=FALSE)`, itemID, spaceID).Scan(&referenceAllowed); err != nil {
				return err
			}
			if !referenceAllowed {
				return ErrLibraryNotFound
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_message_library_references(message_id,space_id,space_library_item_id,created_by_user_id) VALUES($1,$2,$3,$4)`, out.ID, spaceID, itemID, userID); err != nil {
				return err
			}
		}
		if err := tx.QueryRowContext(ctx, `SELECT name,avatar_version FROM users WHERE id=$1`, userID).Scan(&out.SenderName, &out.SenderAvatarVersion); err != nil {
			return err
		}
		out.Sender = SpaceMessageSender{Kind: "person", UserID: userID, DisplayName: out.SenderName, AvatarVersion: out.SenderAvatarVersion}
		eventID, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "message.created", out.ID, out)
		if err != nil {
			return err
		}
		inboxPayload, _ := json.Marshal(map[string]any{"sender_name": out.SenderName, "preview": messagePreview(content), "conversation_id": conversationID})
		recipientsQuery := `SELECT user_id FROM space_members WHERE space_id=$1 AND user_id<>$2`
		recipientArgs := []any{spaceID, userID}
		if conversationID != "" {
			recipientsQuery = `SELECT cm.user_id FROM space_conversation_members cm JOIN space_conversations c ON c.id=cm.conversation_id WHERE c.space_id=$1 AND cm.user_id<>$2 AND cm.conversation_id=$3`
			recipientArgs = append(recipientArgs, conversationID)
		}
		rows, err := tx.QueryContext(ctx, recipientsQuery, recipientArgs...)
		if err != nil {
			return err
		}
		recipientIDs := []string{}
		for rows.Next() {
			var memberID string
			if err := rows.Scan(&memberID); err != nil {
				rows.Close()
				return err
			}
			recipientIDs = append(recipientIDs, memberID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, memberID := range recipientIDs {
			allowed, err := hasSpacePermissionTx(ctx, tx, memberID, spaceID, PermissionMessagesRead)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			kind := "unread"
			if mentionUsers[memberID] {
				kind = "mention"
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_inbox_items(user_id,space_id,kind,message_id,event_id,payload) VALUES($1,$2,$3,$4,$5,$6)`, memberID, spaceID, kind, out.ID, eventID, inboxPayload); err != nil {
				return err
			}
		}
		return nil
	})
	return out, agentMentions, err
}

func uniqueSpaceIDs(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

// pqStringArray intentionally returns a PostgreSQL array literal without
// importing pq into the public data model. IDs are server-generated and cannot
// contain quotes, commas, or braces.
func pqStringArray(values []string) string {
	if len(values) == 0 {
		return "{}"
	}
	return "{" + strings.Join(values, ",") + "}"
}

func scanSpaceMessage(scanner interface{ Scan(...any) error }, out *SpaceMessage) error {
	var raw []byte
	var files string
	var agentID sql.NullString
	var origin []byte
	if err := scanner.Scan(&out.Seq, &out.ID, &out.SpaceID, &out.ConversationID, &out.SenderUserID, &out.SenderName, &out.SenderAvatarVersion, &out.SenderKind, &agentID, &raw, &files, &out.EditedAt, &out.CreatedAt, &out.ReplyToMessageID, &origin); err != nil {
		return err
	}
	out.SenderAgentID = agentID.String
	out.Sender = SpaceMessageSender{Kind: out.SenderKind, DisplayName: out.SenderName, AvatarVersion: out.SenderAvatarVersion}
	if out.SenderKind == "agent" {
		out.Sender.AgentID = out.SenderAgentID
	} else if out.SenderKind == "person" {
		out.Sender.UserID = out.SenderUserID
	}
	if len(origin) > 0 {
		out.Origin = append(json.RawMessage(nil), origin...)
	}
	if err := json.Unmarshal(raw, &out.Content); err != nil {
		return err
	}
	out.FileNodeIDs = parsePGTextArray(files)
	return nil
}

func parsePGTextArray(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return []string{}
	}
	return strings.Split(strings.TrimSuffix(strings.TrimPrefix(raw, "{"), "}"), ",")
}

const spaceMessageColumns = `m.seq,m.id,m.space_id,COALESCE(m.conversation_id,''),m.sender_user_id,CASE WHEN m.origin->>'author_name' IS NOT NULL AND m.origin->>'author_name'<>'' THEN m.origin->>'author_name' WHEN m.sender_kind='agent' THEN COALESCE(a.name,(SELECT v.name FROM personal_agent_space_grants g JOIN personal_agent_versions v ON v.id=g.approved_version_id WHERE g.agent_id=m.sender_agent_id AND g.space_id=m.space_id LIMIT 1),'Former agent') ELSE COALESCE(u.name,'System') END,CASE WHEN m.sender_kind='person' AND COALESCE(m.origin->>'author_name','')='' THEN COALESCE(u.avatar_version,0) ELSE 0 END,m.sender_kind,m.sender_agent_id,m.content,m.file_node_ids::text,m.edited_at,m.created_at,COALESCE(m.reply_to_message_id,''),m.origin`

func (db *Database) SpaceMessages(ctx context.Context, userID, spaceID string, before int64, limit int) ([]SpaceMessage, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	items := []SpaceMessage{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return err
		}
		if err := requireStandardSpaceTx(ctx, tx, spaceID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceMessageColumns+` FROM space_messages m
			LEFT JOIN users u ON u.id=m.sender_user_id LEFT JOIN space_agents a ON a.id=m.sender_agent_id
			WHERE m.space_id=$1 AND m.conversation_id IS NULL AND ($2=0 OR m.seq<$2) ORDER BY m.seq DESC LIMIT $3`, spaceID, before, limit)
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
	return items, err
}
