package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AIToolOutput is what one tool call returned, stored with its conversation for
// its owner. A sealed result has no readable output: only that the call happened.
type AIToolOutput struct {
	InvocationID string          `json:"invocation_id"`
	ToolCallID   string          `json:"tool_call_id"`
	ToolName     string          `json:"tool_name"`
	Sealed       bool            `json:"sealed,omitempty"`
	Truncated    bool            `json:"truncated,omitempty"`
	Output       json.RawMessage `json:"output,omitempty"`
	CreatedAt    time.Time       `json:"created_at,omitempty"`
}

// RecordAIToolOutput stores a tool call's output in its conversation.
func (db *Database) RecordAIToolOutput(ctx context.Context, userID, conversationID string, output AIToolOutput) error {
	data, err := json.Marshal(output)
	if err != nil {
		return err
	}
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO misty_ask_conversation_events(conversation_id,user_id,event_type,data)
			SELECT id,user_id,'tool_result',$3 FROM misty_ask_conversations WHERE id=$1 AND user_id=$2`, conversationID, userID, data)
		return err
	})
}

// AIInvocationToolOutputs is what one run's tools returned, for its owner only.
func (db *Database) AIInvocationToolOutputs(ctx context.Context, userID, invocationID string) ([]AIToolOutput, error) {
	items := []AIToolOutput{}
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT data,created_at FROM misty_ask_conversation_events
			WHERE user_id=$1 AND event_type='tool_result' AND data->>'invocation_id'=$2 ORDER BY id`, userID, invocationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var data []byte
			var item AIToolOutput
			var at time.Time
			if err := rows.Scan(&data, &at); err != nil {
				return err
			}
			if err := json.Unmarshal(data, &item); err != nil {
				return err
			}
			item.CreatedAt = at
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// CreateToolOutputAttachment records a screenshot a tool returned, already stored
// at objectKey. It keeps one object under both keys, and is never offered to the
// model as something the person attached.
func (db *Database) CreateToolOutputAttachment(ctx context.Context, item AIConversationAttachment) (string, error) {
	id := "aiatt_" + uuid.NewString()
	err := db.WithRLSContext(ctx, userRLSSettings(item.UserID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_conversation_attachments(id,user_id,conversation_id,invocation_id,scope,display_name,mime_type,byte_size,sha256,width,height,object_key,model_mime_type,model_byte_size,model_sha256,model_width,model_height,model_object_key,lifecycle_state)
			VALUES($1,$2,$3,$4,'tool_output',$5,$6,$7,$8,$9,$10,$11,$6,$7,$8,$12,$13,$11,'ready')`,
			id, item.UserID, item.ConversationID, item.InvocationID, item.DisplayName, item.MIMEType, item.ByteSize, item.SHA256,
			item.Width, item.Height, item.ObjectKey, min(item.Width, 2048), min(item.Height, 2048))
		return err
	})
	return id, err
}
