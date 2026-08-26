package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type AIConversationPendingAction struct {
	UserID           string          `json:"-"`
	ConversationID   string          `json:"conversation_id"`
	SpaceID          string          `json:"space_id"`
	Intent           string          `json:"intent"`
	TargetKind       string          `json:"target_kind,omitempty"`
	TargetID         string          `json:"target_id,omitempty"`
	TargetLabel      string          `json:"target_label,omitempty"`
	Question         string          `json:"question"`
	OriginalPrompt   string          `json:"original_prompt,omitempty"`
	Evidence         json.RawMessage `json:"evidence"`
	CandidateIntents json.RawMessage `json:"candidate_intents"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

func (db *Database) UpsertAIConversationPendingAction(ctx context.Context, item AIConversationPendingAction) error {
	item.UserID = strings.TrimSpace(item.UserID)
	item.ConversationID = strings.TrimSpace(item.ConversationID)
	item.SpaceID = strings.TrimSpace(item.SpaceID)
	item.Intent = strings.TrimSpace(item.Intent)
	item.TargetKind = strings.TrimSpace(item.TargetKind)
	item.TargetID = strings.TrimSpace(item.TargetID)
	item.TargetLabel = strings.TrimSpace(item.TargetLabel)
	item.Question = strings.TrimSpace(item.Question)
	item.OriginalPrompt = strings.TrimSpace(item.OriginalPrompt)
	if item.UserID == "" || item.ConversationID == "" || item.SpaceID == "" || item.Intent == "" || item.Question == "" ||
		len([]rune(item.Intent)) > 120 || len([]rune(item.Question)) > 1000 || len([]rune(item.OriginalPrompt)) > 20000 {
		return ErrSpaceInvalid
	}
	if len(item.Evidence) == 0 {
		item.Evidence = json.RawMessage(`[]`)
	}
	if len(item.CandidateIntents) == 0 {
		item.CandidateIntents = json.RawMessage(`[]`)
	}
	if !validJSONArray(item.Evidence) || !validJSONArray(item.CandidateIntents) {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(item.UserID), func(tx *sql.Tx) error {
		var member bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_members WHERE user_id=$1 AND space_id=$2)`, item.UserID, item.SpaceID).Scan(&member); err != nil {
			return err
		}
		if !member {
			return ErrSpaceForbidden
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO misty_conversation_pending_actions(
			user_id,conversation_id,space_id,intent,target_kind,target_id,target_label,question,original_prompt,evidence,candidate_intents)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT(user_id,conversation_id,space_id) DO UPDATE SET
				intent=EXCLUDED.intent,target_kind=EXCLUDED.target_kind,target_id=EXCLUDED.target_id,
				target_label=EXCLUDED.target_label,question=EXCLUDED.question,original_prompt=EXCLUDED.original_prompt,
				evidence=EXCLUDED.evidence,candidate_intents=EXCLUDED.candidate_intents,updated_at=NOW()`,
			item.UserID, item.ConversationID, item.SpaceID, item.Intent, item.TargetKind, item.TargetID,
			item.TargetLabel, item.Question, item.OriginalPrompt, item.Evidence, item.CandidateIntents)
		return err
	})
}

func (db *Database) AIConversationPendingAction(ctx context.Context, userID, conversationID, spaceID string) (*AIConversationPendingAction, error) {
	item := &AIConversationPendingAction{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT user_id,conversation_id,space_id,intent,target_kind,target_id,target_label,question,original_prompt,evidence,candidate_intents,created_at,updated_at
			FROM misty_conversation_pending_actions WHERE user_id=$1 AND conversation_id=$2 AND space_id=$3`,
			userID, strings.TrimSpace(conversationID), spaceID).Scan(
			&item.UserID, &item.ConversationID, &item.SpaceID, &item.Intent, &item.TargetKind, &item.TargetID,
			&item.TargetLabel, &item.Question, &item.OriginalPrompt, &item.Evidence, &item.CandidateIntents, &item.CreatedAt, &item.UpdatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return item, err
}

func (db *Database) ClearAIConversationPendingAction(ctx context.Context, userID, conversationID, spaceID string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM misty_conversation_pending_actions WHERE user_id=$1 AND conversation_id=$2 AND space_id=$3`, userID, strings.TrimSpace(conversationID), spaceID)
		return err
	})
}

func validJSONArray(raw json.RawMessage) bool {
	if !json.Valid(raw) {
		return false
	}
	var value []any
	return json.Unmarshal(raw, &value) == nil
}
