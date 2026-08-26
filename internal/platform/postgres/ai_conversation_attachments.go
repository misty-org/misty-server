package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
)

type AIConversationAttachment struct {
	ID, UserID, ConversationID, InvocationID, Scope, DisplayName, MIMEType string
	ByteSize                                                               int64
	SHA256                                                                 string
	Width, Height                                                          int
	ObjectKey, ModelMIMEType                                               string
	ModelByteSize                                                          int64
	ModelSHA256                                                            string
	ModelWidth, ModelHeight                                                int
	ModelObjectKey, LifecycleState                                         string
	ExpiresAt                                                              *time.Time
	CreatedAt, UpdatedAt                                                   time.Time
}

func scanAIConversationAttachment(scanner interface{ Scan(...any) error }, item *AIConversationAttachment) error {
	return scanner.Scan(&item.ID, &item.UserID, &item.ConversationID, &item.InvocationID, &item.Scope,
		&item.DisplayName, &item.MIMEType, &item.ByteSize, &item.SHA256, &item.Width, &item.Height,
		&item.ObjectKey, &item.ModelMIMEType, &item.ModelByteSize, &item.ModelSHA256,
		&item.ModelWidth, &item.ModelHeight, &item.ModelObjectKey, &item.LifecycleState,
		&item.ExpiresAt, &item.CreatedAt, &item.UpdatedAt)
}

const aiConversationAttachmentColumns = `id,user_id,COALESCE(conversation_id,''),COALESCE(invocation_id,''),scope,display_name,mime_type,byte_size,sha256,width,height,object_key,model_mime_type,model_byte_size,model_sha256,model_width,model_height,model_object_key,lifecycle_state,expires_at,created_at,updated_at`

func (db *Database) CreateAIConversationAttachment(ctx context.Context, item AIConversationAttachment) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(item.UserID), func(tx *sql.Tx) error {
		if item.Scope == "conversation" {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL)`, item.ConversationID, item.UserID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return serveragent.ErrPersistedSessionNotFound
			}
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_conversation_attachments WHERE conversation_id=$1 AND user_id=$2 AND invocation_id IS NULL AND lifecycle_state IN ('pending','ready')`, item.ConversationID, item.UserID).Scan(&count); err != nil {
				return err
			}
			if count >= 10 {
				return errors.New("Misty accepts up to 10 images per turn")
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_conversation_attachments(id,user_id,conversation_id,scope,display_name,mime_type,byte_size,sha256,width,height,object_key,model_mime_type,model_byte_size,model_sha256,model_width,model_height,model_object_key,expires_at) VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
			item.ID, item.UserID, item.ConversationID, item.Scope, item.DisplayName, item.MIMEType, item.ByteSize,
			item.SHA256, item.Width, item.Height, item.ObjectKey, item.ModelMIMEType, item.ModelByteSize,
			item.ModelSHA256, item.ModelWidth, item.ModelHeight, item.ModelObjectKey, item.ExpiresAt)
		return err
	})
}

func (db *Database) AIConversationAttachment(ctx context.Context, userID, attachmentID string) (*AIConversationAttachment, error) {
	var item AIConversationAttachment
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return scanAIConversationAttachment(tx.QueryRowContext(ctx, `SELECT `+aiConversationAttachmentColumns+` FROM ai_conversation_attachments WHERE id=$1 AND user_id=$2 AND lifecycle_state<>'deleted'`, attachmentID, userID), &item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return &item, err
}

func (db *Database) CompleteAIConversationAttachment(ctx context.Context, userID, attachmentID string) (*AIConversationAttachment, error) {
	var item AIConversationAttachment
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return scanAIConversationAttachment(tx.QueryRowContext(ctx, `UPDATE ai_conversation_attachments SET lifecycle_state='ready',updated_at=NOW() WHERE id=$1 AND user_id=$2 AND lifecycle_state='pending' RETURNING `+aiConversationAttachmentColumns, attachmentID, userID), &item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return &item, err
}

func (db *Database) DeleteAIConversationAttachment(ctx context.Context, userID, attachmentID string) (*AIConversationAttachment, error) {
	item, err := db.AIConversationAttachment(ctx, userID, attachmentID)
	if err != nil {
		return nil, err
	}
	if item.InvocationID != "" {
		return nil, errors.New("sent attachments cannot be removed from conversation history")
	}
	err = db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM ai_conversation_attachments WHERE id=$1 AND user_id=$2`, attachmentID, userID)
		return err
	})
	return item, err
}

func (db *Database) BindAIConversationAttachments(ctx context.Context, userID, conversationID, invocationID string, attachmentIDs []string) error {
	if len(attachmentIDs) == 0 {
		return nil
	}
	if len(attachmentIDs) > 10 {
		return errors.New("Misty accepts up to 10 images per turn")
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		for _, id := range attachmentIDs {
			result, err := tx.ExecContext(ctx, `UPDATE ai_conversation_attachments SET invocation_id=$1,updated_at=NOW() WHERE id=$2 AND user_id=$3 AND conversation_id=$4 AND scope='conversation' AND lifecycle_state='ready' AND invocation_id IS NULL`, invocationID, id, userID, conversationID)
			if err != nil {
				return err
			}
			if rows, _ := result.RowsAffected(); rows != 1 {
				return errors.New("invalid Misty attachment")
			}
		}
		return nil
	})
}

func (db *Database) ValidateAIConversationAttachments(ctx context.Context, userID, conversationID string, attachmentIDs []string) error {
	if len(attachmentIDs) == 0 {
		return nil
	}
	if len(attachmentIDs) > 10 {
		return errors.New("Misty accepts up to 10 images per turn")
	}
	seen := map[string]bool{}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		for _, id := range attachmentIDs {
			if id == "" || seen[id] {
				return errors.New("invalid Misty attachment")
			}
			seen[id] = true
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_conversation_attachments WHERE id=$1 AND user_id=$2 AND conversation_id=$3 AND scope='conversation' AND lifecycle_state='ready' AND invocation_id IS NULL)`, id, userID, conversationID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return errors.New("invalid Misty attachment")
			}
		}
		return nil
	})
}

func (db *Database) AIConversationAttachmentsForInvocation(ctx context.Context, userID, invocationID string) ([]AIConversationAttachment, error) {
	items := []AIConversationAttachment{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+aiConversationAttachmentColumns+` FROM ai_conversation_attachments WHERE user_id=$1 AND invocation_id=$2 AND lifecycle_state='ready' ORDER BY created_at`, userID, invocationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AIConversationAttachment
			if err := scanAIConversationAttachment(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) AIConversationAttachmentObjectKeys(ctx context.Context, userID, conversationID string) ([]string, error) {
	keys := []string{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT object_key,model_object_key FROM ai_conversation_attachments WHERE user_id=$1 AND conversation_id=$2`, userID, conversationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var original, model string
			if err := rows.Scan(&original, &model); err != nil {
				return err
			}
			keys = append(keys, original, model)
		}
		return rows.Err()
	})
	return keys, err
}

func (db *Database) DeleteExpiredAIConversationAttachments(ctx context.Context, userID string) ([]string, error) {
	keys := []string{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `DELETE FROM ai_conversation_attachments WHERE user_id=$1 AND invocation_id IS NULL AND ((expires_at IS NOT NULL AND expires_at<NOW()) OR (lifecycle_state='pending' AND created_at<NOW()-INTERVAL '24 hours')) RETURNING object_key,model_object_key`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var original, model string
			if err := rows.Scan(&original, &model); err != nil {
				return err
			}
			keys = append(keys, original, model)
		}
		return rows.Err()
	})
	return keys, err
}
