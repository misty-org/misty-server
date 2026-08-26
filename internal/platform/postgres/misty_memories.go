package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type MistyMemory struct {
	ID                   string     `json:"id"`
	SpaceID              string     `json:"space_id,omitempty"`
	Kind                 string     `json:"kind"`
	Content              string     `json:"content"`
	Reason               string     `json:"reason,omitempty"`
	SourceConversationID string     `json:"source_conversation_id,omitempty"`
	SourceInvocationID   string     `json:"source_invocation_id,omitempty"`
	LastUsedAt           *time.Time `json:"last_used_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type RememberMistyMemoryInput struct {
	SpaceID              string
	Kind                 string
	Content              string
	Reason               string
	SourceConversationID string
	SourceInvocationID   string
}

const maxMistyMemoryList = 100

func (db *Database) RememberMistyMemory(ctx context.Context, userID string, input RememberMistyMemoryInput) (MistyMemory, error) {
	input.SpaceID = strings.TrimSpace(input.SpaceID)
	input.Kind = strings.ToLower(strings.TrimSpace(input.Kind))
	input.Content = strings.TrimSpace(input.Content)
	input.Reason = strings.TrimSpace(input.Reason)
	input.SourceConversationID = strings.TrimSpace(input.SourceConversationID)
	input.SourceInvocationID = strings.TrimSpace(input.SourceInvocationID)
	if input.Kind == "" {
		input.Kind = "fact"
	}
	if !validMistyMemoryKind(input.Kind) || input.Content == "" || len([]rune(input.Content)) > 1000 || len([]rune(input.Reason)) > 500 {
		return MistyMemory{}, ErrSpaceInvalid
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(input.Content), " "))
	digest := sha256.Sum256([]byte(normalized))
	memoryKey := hex.EncodeToString(digest[:])
	scopeKey := "personal"
	if input.SpaceID != "" {
		scopeKey = "space:" + input.SpaceID
	}
	item := MistyMemory{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		enabled, err := mistyMemoryEnabledTx(ctx, tx, userID)
		if err != nil || !enabled {
			if err != nil {
				return err
			}
			return ErrSpaceConflict
		}
		if input.SpaceID != "" {
			if _, err := requireSpaceMemberTx(ctx, tx, input.SpaceID, userID); err != nil {
				return err
			}
		}
		conversationID, err := ownedMistyMemoryConversationTx(ctx, tx, userID, input.SourceConversationID)
		if err != nil {
			return err
		}
		invocationID, err := ownedMistyMemoryInvocationTx(ctx, tx, userID, input.SourceInvocationID)
		if err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `
			INSERT INTO misty_memories(
				id,user_id,space_id,scope_key,memory_key,kind,content,reason,
				source_conversation_id,source_invocation_id
			) VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,NULLIF($9,''),NULLIF($10,''))
			ON CONFLICT(user_id,scope_key,memory_key) DO UPDATE SET
				kind=EXCLUDED.kind,content=EXCLUDED.content,reason=EXCLUDED.reason,
				source_conversation_id=COALESCE(EXCLUDED.source_conversation_id,misty_memories.source_conversation_id),
				source_invocation_id=COALESCE(EXCLUDED.source_invocation_id,misty_memories.source_invocation_id),
				forgotten_at=NULL,updated_at=NOW()
			RETURNING id,COALESCE(space_id,''),kind,content,reason,
				COALESCE(source_conversation_id,''),COALESCE(source_invocation_id,''),
				last_used_at,created_at,updated_at
		`, "memory_"+uuid.NewString(), userID, input.SpaceID, scopeKey, memoryKey,
			input.Kind, input.Content, input.Reason, conversationID, invocationID,
		).Scan(&item.ID, &item.SpaceID, &item.Kind, &item.Content, &item.Reason,
			&item.SourceConversationID, &item.SourceInvocationID, &item.LastUsedAt, &item.CreatedAt, &item.UpdatedAt)
	})
	return item, err
}

func (db *Database) MistyMemories(ctx context.Context, userID, spaceID string, limit int) ([]MistyMemory, error) {
	spaceID = strings.TrimSpace(spaceID)
	if limit < 1 || limit > maxMistyMemoryList {
		limit = maxMistyMemoryList
	}
	items := []MistyMemory{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if spaceID != "" {
			if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT id,COALESCE(space_id,''),kind,content,reason,
				COALESCE(source_conversation_id,''),COALESCE(source_invocation_id,''),
				last_used_at,created_at,updated_at
			FROM misty_memories
			WHERE user_id=$1 AND forgotten_at IS NULL
				AND ($2='' OR space_id IS NULL OR space_id=$2)
			ORDER BY (space_id=$2) DESC,updated_at DESC LIMIT $3
		`, userID, spaceID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item MistyMemory
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.Kind, &item.Content, &item.Reason,
				&item.SourceConversationID, &item.SourceInvocationID, &item.LastUsedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) MistyMemoryContext(ctx context.Context, userID, spaceID string, limit int) ([]MistyMemory, error) {
	items := []MistyMemory{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		enabled, err := mistyMemoryEnabledTx(ctx, tx, userID)
		if err != nil || !enabled {
			return err
		}
		if spaceID != "" {
			if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
				return err
			}
		}
		if limit < 1 || limit > 30 {
			limit = 20
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT id,COALESCE(space_id,''),kind,content,reason,
				COALESCE(source_conversation_id,''),COALESCE(source_invocation_id,''),
				last_used_at,created_at,updated_at
			FROM misty_memories
			WHERE user_id=$1 AND forgotten_at IS NULL
				AND (space_id IS NULL OR ($2<>'' AND space_id=$2))
			ORDER BY (space_id=$2) DESC,updated_at DESC LIMIT $3
		`, userID, spaceID, limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			var item MistyMemory
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.Kind, &item.Content, &item.Reason,
				&item.SourceConversationID, &item.SourceInvocationID, &item.LastUsedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
				rows.Close()
				return err
			}
			items = append(items, item)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(items) > 0 {
			ids := make([]string, 0, len(items))
			for _, item := range items {
				ids = append(ids, item.ID)
			}
			_, err = tx.ExecContext(ctx, `UPDATE misty_memories SET last_used_at=NOW() WHERE user_id=$1 AND id=ANY($2)`, userID, pq.Array(ids))
		}
		return err
	})
	return items, err
}

func (db *Database) ForgetMistyMemory(ctx context.Context, userID, memoryID string) error {
	memoryID = strings.TrimSpace(memoryID)
	if memoryID == "" {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE misty_memories SET forgotten_at=NOW(),updated_at=NOW() WHERE id=$1 AND user_id=$2 AND forgotten_at IS NULL`, memoryID, userID)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			return ErrSpaceNotFound
		}
		return nil
	})
}

func mistyMemoryEnabledTx(ctx context.Context, tx *sql.Tx, userID string) (bool, error) {
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT enabled AND memory_enabled FROM ai_user_settings WHERE user_id=$1),TRUE)`, userID).Scan(&enabled)
	return enabled, err
}

func validMistyMemoryKind(kind string) bool {
	return kind == "fact" || kind == "preference" || kind == "instruction"
}

func ownedMistyMemoryConversationTx(ctx context.Context, tx *sql.Tx, userID, conversationID string) (string, error) {
	if conversationID == "" {
		return "", nil
	}
	var owned string
	err := tx.QueryRowContext(ctx, `SELECT id FROM agent_conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, conversationID, userID).Scan(&owned)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSpaceInvalid
	}
	return owned, err
}

func ownedMistyMemoryInvocationTx(ctx context.Context, tx *sql.Tx, userID, invocationID string) (string, error) {
	if invocationID == "" {
		return "", nil
	}
	var owned string
	err := tx.QueryRowContext(ctx, `SELECT id FROM ai_invocations WHERE id=$1 AND user_id=$2`, invocationID, userID).Scan(&owned)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSpaceInvalid
	}
	return owned, err
}
