package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// UpdateSpaceDiscordLinkDirection changes the allowed mirror direction without
// discarding the cursor, so pausing a mirror never replays a transcript.
func (db *Database) UpdateSpaceDiscordLinkDirection(ctx context.Context, userID, spaceID, linkID, direction string) (*SpaceDiscordLink, error) {
	if direction != "two_way" && direction != "inbound" && direction != "outbound" {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceDiscordLink{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		err := scanSpaceDiscordLink(tx.QueryRowContext(ctx, `UPDATE space_discord_links
			SET direction=$1,updated_at=NOW() WHERE id=$2 AND space_id=$3 AND disabled_at IS NULL
			RETURNING `+spaceDiscordLinkColumns, direction, linkID, spaceID), out)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteSpaceDiscordLink unlinks a channel. Already-mirrored messages stay in
// the Space: unlinking stops future mirroring, it does not rewrite history.
func (db *Database) DeleteSpaceDiscordLink(ctx context.Context, userID, spaceID, linkID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		var conversationID string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(conversation_id,'') FROM space_discord_links
			WHERE id=$1 AND space_id=$2`, linkID, spaceID).Scan(&conversationID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceNotFound
			}
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_discord_links
			SET disabled_at=NOW(),status='disabled',webhook_id='',
			    webhook_token_ciphertext=NULL,webhook_token_nonce=NULL,updated_at=NOW()
			WHERE id=$1 AND space_id=$2 AND disabled_at IS NULL`, linkID, spaceID)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return ErrSpaceNotFound
		}
		if conversationID != "" {
			_, err = tx.ExecContext(ctx, `UPDATE space_conversations
				SET integration_status='disconnected',updated_at=NOW() WHERE id=$1`, conversationID)
		}
		return err
	})
}

// SetSpaceDiscordLinkSync records the outcome of a sync pass. The cursor only
// ever moves forward, so a retry or an out-of-order page cannot replay history.
func (db *Database) SetSpaceDiscordLinkSync(ctx context.Context, linkID, cursor, status, errorCode string, syncedAt *time.Time) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_discord_links
			SET last_message_id=CASE WHEN $2<>'' THEN $2 ELSE last_message_id END,
			    status=$3,last_error_code=$4,
			    last_synced_at=COALESCE($5,last_synced_at),updated_at=NOW()
			WHERE id=$1`, linkID, cursor, status, errorCode, syncedAt)
		return err
	})
}

func (db *Database) UpdateSpaceDiscordLinkDisplay(
	ctx context.Context,
	linkID, channelName string,
) error {
	channelName = strings.TrimSpace(channelName)
	if channelName == "" {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var conversationID string
		if err := tx.QueryRowContext(ctx, `UPDATE space_discord_links
			SET channel_name=$1,updated_at=NOW() WHERE id=$2
			RETURNING COALESCE(conversation_id,'')`, channelName, linkID).Scan(&conversationID); err != nil {
			return err
		}
		if conversationID == "" {
			return nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE space_conversations
			SET title=$1,external_display_name=$2,updated_at=NOW() WHERE id=$3`,
			discordConversationTitle(channelName), channelName, conversationID)
		return err
	})
}

// CreateMirroredSpaceMessage writes an inbound Discord message into a Space.
//
// It is deliberately idempotent on the provider's message id: the Gateway, a
// manual sync, and a retry can all deliver the same message, and a mirrored
// transcript that duplicates lines is worse than one that lags.
func (db *Database) CreateMirroredSpaceMessage(ctx context.Context, link SpaceDiscordLink, content []MessageSpan, origin MessageOrigin) (*SpaceMessage, error) {
	if err := TestingValidateMessage(content, nil); err != nil {
		return nil, err
	}
	if origin.System != "discord" || strings.TrimSpace(origin.ExternalID) == "" {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceMessage{ID: "msg_" + uuid.NewString(), SpaceID: link.SpaceID, ConversationID: link.ConversationID,
		SenderUserID: link.ConnectedByUserID, SenderKind: "person", SenderName: origin.AuthorName,
		Content: content, FileNodeIDs: []string{}, LibraryItemIDs: []string{}, Attachments: []MessageAttachment{}}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		// Gateway delivery and manual sync can race. Serialize this provider
		// identity so the duplicate check and insert form one atomic operation.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, link.SpaceID+":"+origin.ExternalID); err != nil {
			return err
		}
		var duplicate bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_messages
			WHERE space_id=$1 AND origin->>'system'='discord' AND origin->>'external_id'=$2)`,
			link.SpaceID, origin.ExternalID).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate {
			return errDiscordMessageAlreadyMirrored
		}
		if origin.ReplyToExternalID != "" {
			replyErr := tx.QueryRowContext(ctx, `SELECT id FROM space_messages
				WHERE space_id=$1 AND conversation_id=$2 AND origin->>'system'='discord'
				  AND origin->>'external_id'=$3 ORDER BY seq DESC LIMIT 1`,
				link.SpaceID, link.ConversationID, origin.ReplyToExternalID).Scan(&out.ReplyToMessageID)
			if replyErr != nil && !errors.Is(replyErr, sql.ErrNoRows) {
				return replyErr
			}
		}
		raw, _ := json.Marshal(content)
		originRaw, _ := json.Marshal(origin)
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_messages(id,space_id,conversation_id,sender_user_id,sender_kind,content,reply_to_message_id,origin)
			VALUES($1,$2,NULLIF($3,''),$4,'person',$5,NULLIF($6,''),$7) RETURNING seq,created_at`,
			out.ID, link.SpaceID, link.ConversationID, link.ConnectedByUserID, raw, out.ReplyToMessageID, originRaw).Scan(&out.Seq, &out.CreatedAt); err != nil {
			return err
		}
		out.Origin = originRaw
		_, err := recordSpaceEventTx(ctx, tx, link.SpaceID, link.ConnectedByUserID, "message.created", out.ID, out)
		return err
	})
	if errors.Is(err, errDiscordMessageAlreadyMirrored) {
		return nil, ErrSpaceConflict
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

var errDiscordMessageAlreadyMirrored = errors.New("discord message is already mirrored")

// SpaceMessageForPublish reads one message the caller owns, for mirroring out.
func (db *Database) SpaceMessageForPublish(ctx context.Context, userID, spaceID, messageID string) (*SpaceMessage, error) {
	out := &SpaceMessage{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		return scanSpaceMessage(tx.QueryRowContext(ctx, `SELECT `+spaceMessageColumns+` FROM space_messages m
			LEFT JOIN users u ON u.id=m.sender_user_id LEFT JOIN space_agents a ON a.id=m.sender_agent_id
			WHERE m.id=$1 AND m.space_id=$2`, messageID, spaceID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ClaimSpaceMessageDiscordPublish prevents concurrent retries from posting the
// same Misty message twice. A failed provider call replaces this temporary
// state with "failed", which makes an intentional later retry claimable again.
func (db *Database) ClaimSpaceMessageDiscordPublish(ctx context.Context, userID, spaceID, messageID, channelID string) (bool, error) {
	if userID == "" || spaceID == "" || messageID == "" || channelID == "" {
		return false, ErrSpaceInvalid
	}
	origin, _ := json.Marshal(MessageOrigin{
		System: "misty", PublishState: "publishing", ExternalChannelID: channelID,
	})
	claimed := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_messages SET origin=$1
			WHERE id=$2 AND space_id=$3 AND sender_kind='person' AND sender_user_id=$4
			  AND COALESCE(origin->>'system','') IN ('','misty')
			  AND COALESCE(origin->>'published_external_id','')=''
			  AND COALESCE(origin->>'publish_state','') NOT IN ('publishing','published')`,
			origin, messageID, spaceID, userID)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		claimed = err == nil && changed == 1
		return err
	})
	return claimed, err
}

// SetSpaceMessageOrigin stamps the outcome of an outward publish onto a
// message, so the transcript shows what actually reached Discord.
func (db *Database) SetSpaceMessageOrigin(ctx context.Context, spaceID, messageID string, origin MessageOrigin) (*SpaceMessage, error) {
	out := &SpaceMessage{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		originRaw, _ := json.Marshal(origin)
		if _, err := tx.ExecContext(ctx, `UPDATE space_messages SET origin=$1 WHERE id=$2 AND space_id=$3`,
			originRaw, messageID, spaceID); err != nil {
			return err
		}
		return scanSpaceMessage(tx.QueryRowContext(ctx, `SELECT `+spaceMessageColumns+` FROM space_messages m
			LEFT JOIN users u ON u.id=m.sender_user_id LEFT JOIN space_agents a ON a.id=m.sender_agent_id
			WHERE m.id=$1 AND m.space_id=$2`, messageID, spaceID), out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DiscordExternalReplyID resolves a Misty reply target only when its source is
// a Discord message in the same external channel. This prevents a local reply
// id from being used to reference an unrelated server or channel.
func (db *Database) DiscordExternalReplyID(ctx context.Context, spaceID, messageID, channelID string) (string, error) {
	if spaceID == "" || messageID == "" || channelID == "" {
		return "", nil
	}
	var externalID string
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT CASE WHEN origin->>'system'='discord'
			THEN COALESCE(origin->>'external_id','') ELSE COALESCE(origin->>'published_external_id','') END
			FROM space_messages WHERE id=$1 AND space_id=$2
			  AND origin->>'external_channel_id'=$3
			  AND origin->>'system' IN ('discord','misty')`, messageID, spaceID, channelID).Scan(&externalID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return externalID, err
}
