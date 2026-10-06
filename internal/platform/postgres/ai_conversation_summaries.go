package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"unicode/utf8"
)

// AIConversationSummary covers a conversation's turns up to and including
// ThroughInvocationID; later turns are sent verbatim.
type AIConversationSummary struct {
	ConversationID      string
	ThroughInvocationID string
	ThroughCreatedAt    time.Time
	SummarizedTurns     int
	Summary             string
	Model               string
}

// AIConversationSummary returns the conversation's summary, or nil before any
// turns were summarized.
func (db *Database) AIConversationSummary(ctx context.Context, userID, conversationID string) (*AIConversationSummary, error) {
	out := &AIConversationSummary{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT conversation_id,through_invocation_id,through_created_at,summarized_turns,summary,model
			FROM ai_conversation_summaries WHERE conversation_id=$1 AND user_id=$2`, conversationID, userID).
			Scan(&out.ConversationID, &out.ThroughInvocationID, &out.ThroughCreatedAt, &out.SummarizedTurns, &out.Summary, &out.Model)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SaveAIConversationSummary stores a summary that reaches further than the
// stored one. A slower writer cannot replace a newer, longer summary.
func (db *Database) SaveAIConversationSummary(ctx context.Context, userID string, summary AIConversationSummary) error {
	if summary.ConversationID == "" || summary.ThroughInvocationID == "" || summary.SummarizedTurns < 1 || summary.Summary == "" || utf8.RuneCountInString(summary.Summary) > 40_000 {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_conversation_summaries(conversation_id,user_id,through_invocation_id,through_created_at,summarized_turns,summary,model)
			SELECT $1,$2,$3,$4,$5,$6,$7 WHERE EXISTS(SELECT 1 FROM misty_ask_conversations WHERE id=$1 AND user_id=$2)
			ON CONFLICT (conversation_id) DO UPDATE SET through_invocation_id=EXCLUDED.through_invocation_id,through_created_at=EXCLUDED.through_created_at,
				summarized_turns=EXCLUDED.summarized_turns,summary=EXCLUDED.summary,model=EXCLUDED.model,updated_at=now()
			WHERE ai_conversation_summaries.user_id=EXCLUDED.user_id AND ai_conversation_summaries.summarized_turns<EXCLUDED.summarized_turns`,
			summary.ConversationID, userID, summary.ThroughInvocationID, summary.ThroughCreatedAt, summary.SummarizedTurns, summary.Summary, summary.Model)
		return err
	})
}
