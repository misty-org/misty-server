package db

import (
	"context"
	"database/sql"
)

func (db *Database) MarkSpaceConversationRead(
	ctx context.Context, userID, spaceID, conversationID string, seq int64,
) error {
	if seq < 0 {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceConversationMemberTx(
			ctx, tx, userID, spaceID, conversationID,
		); err != nil {
			return err
		}
		var maximum int64
		if err := tx.QueryRowContext(
			ctx, `SELECT COALESCE(MAX(seq),0) FROM space_messages WHERE conversation_id=$1`,
			conversationID,
		).Scan(&maximum); err != nil {
			return err
		}
		if seq == 0 || seq > maximum {
			seq = maximum
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO space_conversation_reads(
			conversation_id,user_id,read_message_seq
		) VALUES($1,$2,$3) ON CONFLICT(conversation_id,user_id) DO UPDATE
		SET read_message_seq=GREATEST(
			space_conversation_reads.read_message_seq,excluded.read_message_seq
		),updated_at=NOW()`, conversationID, userID, seq)
		return err
	})
}
