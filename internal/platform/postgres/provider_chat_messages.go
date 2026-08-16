package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

type ProviderMirroredMessage struct {
	SpaceID, ConversationID, ConnectedByUserID, Provider string
	Content                                              []MessageSpan
	Origin                                               MessageOrigin
}

// UpsertProviderMirroredMessage is the durable inbound edge for provider chat.
// Provider message IDs are the idempotency key, edits update in place, and a
// thread timestamp resolves to Misty's native reply relationship when its root
// has already arrived.
func (db *Database) UpsertProviderMirroredMessage(ctx context.Context, input ProviderMirroredMessage) (*SpaceMessage, bool, error) {
	if input.Provider == "" || input.Origin.System != input.Provider || input.Origin.ExternalID == "" ||
		input.SpaceID == "" || input.ConversationID == "" || input.ConnectedByUserID == "" {
		return nil, false, ErrSpaceInvalid
	}
	if err := TestingValidateMessage(input.Content, nil); err != nil {
		return nil, false, err
	}
	created := false
	out := &SpaceMessage{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`,
			input.SpaceID+":"+input.Provider+":"+input.Origin.ExternalID); err != nil {
			return err
		}
		var existingID string
		err := tx.QueryRowContext(ctx, `SELECT id FROM space_messages
			WHERE space_id=$1 AND origin->>'system'=$2 AND origin->>'external_id'=$3`,
			input.SpaceID, input.Provider, input.Origin.ExternalID).Scan(&existingID)
		contentRaw, _ := json.Marshal(input.Content)
		originRaw, _ := json.Marshal(input.Origin)
		if err == nil {
			if _, err := tx.ExecContext(ctx, `UPDATE space_messages SET content=$1,origin=$2,
				edited_at=NOW() WHERE id=$3`, contentRaw, originRaw, existingID); err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		} else {
			created = true
			existingID = "msg_" + uuid.NewString()
			var replyTo string
			if input.Origin.ExternalThreadID != "" && input.Origin.ExternalThreadID != input.Origin.ExternalID {
				_ = tx.QueryRowContext(ctx, `SELECT id FROM space_messages
					WHERE space_id=$1 AND origin->>'system'=$2 AND origin->>'external_id'=$3`,
					input.SpaceID, input.Provider, input.Origin.ExternalThreadID).Scan(&replyTo)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_messages
				(id,space_id,conversation_id,sender_user_id,sender_kind,content,origin,reply_to_message_id)
				VALUES($1,$2,$3,$4,'person',$5,$6,NULLIF($7,''))`, existingID, input.SpaceID,
				input.ConversationID, input.ConnectedByUserID, contentRaw, originRaw, replyTo); err != nil {
				return err
			}
		}
		if err := scanSpaceMessage(tx.QueryRowContext(ctx, `SELECT `+spaceMessageColumns+`
			FROM space_messages m LEFT JOIN users u ON u.id=m.sender_user_id
			LEFT JOIN space_agents a ON a.id=m.sender_agent_id WHERE m.id=$1`, existingID), out); err != nil {
			return err
		}
		eventType := "message.updated"
		if created {
			eventType = "message.created"
		}
		_, err = recordSpaceEventTx(ctx, tx, input.SpaceID, input.ConnectedByUserID,
			eventType, existingID, out)
		return err
	})
	return out, created, err
}
