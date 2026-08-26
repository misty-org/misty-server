package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type AIConversationFocus struct {
	UserID         string          `json:"-"`
	ConversationID string          `json:"conversation_id"`
	SpaceID        string          `json:"space_id"`
	EntityKind     string          `json:"entity_kind"`
	EntityID       string          `json:"entity_id"`
	Label          string          `json:"label,omitempty"`
	Metadata       json.RawMessage `json:"metadata"`
	SourceTool     string          `json:"source_tool,omitempty"`
	SourceRunID    string          `json:"source_run_id,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

var validAIConversationFocusKinds = map[string]bool{
	"task": true, "person": true, "note": true, "drawing": true,
	"calendar_event": true, "roadmap": true, "library_item": true, "message": true,
}

func (db *Database) UpsertAIConversationFocus(ctx context.Context, focus AIConversationFocus) error {
	focus.UserID = strings.TrimSpace(focus.UserID)
	focus.ConversationID = strings.TrimSpace(focus.ConversationID)
	focus.SpaceID = strings.TrimSpace(focus.SpaceID)
	focus.EntityKind = strings.TrimSpace(focus.EntityKind)
	focus.EntityID = strings.TrimSpace(focus.EntityID)
	focus.Label = strings.TrimSpace(focus.Label)
	if focus.UserID == "" || focus.ConversationID == "" || focus.SpaceID == "" || focus.EntityID == "" || !validAIConversationFocusKinds[focus.EntityKind] || len([]rune(focus.Label)) > 500 {
		return ErrSpaceInvalid
	}
	if len(focus.Metadata) == 0 {
		focus.Metadata = json.RawMessage(`{}`)
	}
	if !validJSONObject(focus.Metadata) {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(focus.UserID), func(tx *sql.Tx) error {
		var member bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_members WHERE user_id=$1 AND space_id=$2)`, focus.UserID, focus.SpaceID).Scan(&member); err != nil {
			return err
		}
		if !member {
			return ErrSpaceForbidden
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO misty_conversation_focus(
			user_id,conversation_id,space_id,entity_kind,entity_id,label,metadata,source_tool,source_run_id)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT(user_id,conversation_id,space_id,entity_kind) DO UPDATE SET
				entity_id=EXCLUDED.entity_id,label=EXCLUDED.label,metadata=EXCLUDED.metadata,
				source_tool=EXCLUDED.source_tool,source_run_id=EXCLUDED.source_run_id,updated_at=NOW()`,
			focus.UserID, focus.ConversationID, focus.SpaceID, focus.EntityKind, focus.EntityID,
			focus.Label, focus.Metadata, strings.TrimSpace(focus.SourceTool), strings.TrimSpace(focus.SourceRunID))
		return err
	})
}

func (db *Database) AIConversationFocuses(ctx context.Context, userID, conversationID, spaceID string) ([]AIConversationFocus, error) {
	items := []AIConversationFocus{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT user_id,conversation_id,space_id,entity_kind,entity_id,label,metadata,source_tool,source_run_id,created_at,updated_at
			FROM misty_conversation_focus WHERE user_id=$1 AND conversation_id=$2 AND space_id=$3
			ORDER BY updated_at DESC,entity_kind`, userID, strings.TrimSpace(conversationID), spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AIConversationFocus
			if err := rows.Scan(&item.UserID, &item.ConversationID, &item.SpaceID, &item.EntityKind, &item.EntityID, &item.Label, &item.Metadata, &item.SourceTool, &item.SourceRunID, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) AIConversationFocusByKind(ctx context.Context, userID, conversationID, spaceID, entityKind string) (*AIConversationFocus, error) {
	item := &AIConversationFocus{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT user_id,conversation_id,space_id,entity_kind,entity_id,label,metadata,source_tool,source_run_id,created_at,updated_at
			FROM misty_conversation_focus WHERE user_id=$1 AND conversation_id=$2 AND space_id=$3 AND entity_kind=$4`,
			userID, strings.TrimSpace(conversationID), spaceID, strings.TrimSpace(entityKind)).Scan(
			&item.UserID, &item.ConversationID, &item.SpaceID, &item.EntityKind, &item.EntityID, &item.Label,
			&item.Metadata, &item.SourceTool, &item.SourceRunID, &item.CreatedAt, &item.UpdatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return item, err
}

func (db *Database) ClearAIConversationFocus(ctx context.Context, userID, conversationID, spaceID, entityKind string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM misty_conversation_focus WHERE user_id=$1 AND conversation_id=$2 AND space_id=$3 AND ($4='' OR entity_kind=$4)`, userID, strings.TrimSpace(conversationID), spaceID, strings.TrimSpace(entityKind))
		return err
	})
}
