package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
)

type SpaceThread struct {
	Root        SpaceMessage `json:"root"`
	ReplyCount  int          `json:"reply_count"`
	LastReplyAt time.Time    `json:"last_reply_at"`
	UnreadCount int          `json:"unread_count"`
}

func followThreadTx(ctx context.Context, tx *sql.Tx, userID, rootID string, seq int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO space_thread_reads(user_id,thread_root_id,read_message_seq) VALUES($1,$2,$3)
		ON CONFLICT(user_id,thread_root_id) DO UPDATE
		SET read_message_seq=GREATEST(space_thread_reads.read_message_seq,excluded.read_message_seq),updated_at=NOW()`, userID, rootID, seq); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO space_thread_reads(user_id,thread_root_id,read_message_seq)
		SELECT m.sender_user_id,m.id,m.seq FROM space_messages m
		JOIN space_members sm ON sm.space_id=m.space_id AND sm.user_id=m.sender_user_id
		WHERE m.id=$1 ON CONFLICT(user_id,thread_root_id) DO NOTHING`, rootID)
	return err
}

func threadAccessTx(ctx context.Context, tx *sql.Tx, userID, spaceID, conversationID string) error {
	if conversationID == "" {
		return requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead)
	}
	return requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID)
}

func threadRootTx(ctx context.Context, tx *sql.Tx, spaceID, rootID string) (conversationID string, seq int64, err error) {
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(conversation_id,''),seq FROM space_messages WHERE id=$1 AND space_id=$2 AND thread_root_id IS NULL`, rootID, spaceID).Scan(&conversationID, &seq)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, ErrSpaceNotFound
	}
	return conversationID, seq, err
}

func loadThreadSummariesTx(ctx context.Context, tx *sql.Tx, items []SpaceMessage) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, len(items))
	for index := range items {
		ids[index] = items[index].ID
	}
	rows, err := tx.QueryContext(ctx, `SELECT thread_root_id,count(*),max(created_at) FROM space_messages WHERE thread_root_id=ANY($1) GROUP BY thread_root_id`, pq.Array(ids))
	if err != nil {
		return err
	}
	defer rows.Close()
	type summary struct {
		count int
		last  time.Time
	}
	found := map[string]summary{}
	for rows.Next() {
		var id string
		var item summary
		if err := rows.Scan(&id, &item.count, &item.last); err != nil {
			return err
		}
		found[id] = item
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for index := range items {
		if item, ok := found[items[index].ID]; ok {
			last := item.last
			items[index].ReplyCount, items[index].LastReplyAt = item.count, &last
		}
	}
	return nil
}

func (db *Database) SpaceThreadReplies(ctx context.Context, userID, spaceID, rootID string, before int64, limit int) ([]SpaceMessage, bool, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	items := []SpaceMessage{}
	hasOlder := false
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		conversationID, _, err := threadRootTx(ctx, tx, spaceID, rootID)
		if err != nil {
			return err
		}
		if err := threadAccessTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return err
		}
		query := `SELECT ` + spaceMessageColumns + ` FROM space_messages m
			LEFT JOIN users u ON u.id=m.sender_user_id
			WHERE m.space_id=$1 AND m.thread_root_id=$2 AND m.seq<$3 ORDER BY m.seq DESC LIMIT $4`
		if before <= 0 {
			query = `SELECT ` + spaceMessageColumns + ` FROM space_messages m
				LEFT JOIN users u ON u.id=m.sender_user_id
				WHERE m.space_id=$1 AND m.thread_root_id=$2 ORDER BY m.seq DESC LIMIT $3`
		}
		var rows *sql.Rows
		if before <= 0 {
			rows, err = tx.QueryContext(ctx, query, spaceID, rootID, limit+1)
		} else {
			rows, err = tx.QueryContext(ctx, query, spaceID, rootID, before, limit+1)
		}
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
		if len(items) > limit {
			hasOlder = true
			items = items[:limit]
		}
		for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
			items[left], items[right] = items[right], items[left]
		}
		for index := range items {
			if err := loadSpaceMessageReferencesTx(ctx, tx, &items[index], userID); err != nil {
				return err
			}
		}
		return nil
	})
	return items, hasOlder, err
}

func (db *Database) SpaceThreadRepliesAfter(ctx context.Context, userID, spaceID, rootID string, after int64, limit int) ([]SpaceMessage, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	items := []SpaceMessage{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		conversationID, _, err := threadRootTx(ctx, tx, spaceID, rootID)
		if err != nil {
			return err
		}
		if err := threadAccessTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceMessageColumns+` FROM space_messages m
			LEFT JOIN users u ON u.id=m.sender_user_id
			WHERE m.space_id=$1 AND m.thread_root_id=$2 AND m.seq>$3 ORDER BY m.seq ASC LIMIT $4`, spaceID, rootID, after, limit)
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

func (db *Database) MarkSpaceThreadRead(ctx context.Context, userID, spaceID, rootID string, seq int64) error {
	if seq < 0 {
		return ErrSpaceInvalid
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		conversationID, rootSeq, err := threadRootTx(ctx, tx, spaceID, rootID)
		if err != nil {
			return err
		}
		if err := threadAccessTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return err
		}
		var maximum int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),$2) FROM space_messages WHERE thread_root_id=$1`, rootID, rootSeq).Scan(&maximum); err != nil {
			return err
		}
		if seq == 0 || seq > maximum {
			seq = maximum
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO space_thread_reads(user_id,thread_root_id,read_message_seq) VALUES($1,$2,$3)
			ON CONFLICT(user_id,thread_root_id) DO UPDATE
			SET read_message_seq=GREATEST(space_thread_reads.read_message_seq,excluded.read_message_seq),updated_at=NOW()`, userID, rootID, seq)
		return err
	})
}

func (db *Database) SpaceThreads(ctx context.Context, userID, spaceID string, limit int) ([]SpaceThread, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	out := []SpaceThread{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceMessageColumns+`,t.n,t.last,t.unread
			FROM space_thread_reads f
			JOIN space_messages m ON m.id=f.thread_root_id AND m.space_id=$2
			LEFT JOIN users u ON u.id=m.sender_user_id
			JOIN LATERAL (SELECT count(*) AS n,max(r.created_at) AS last,
				count(*) FILTER (WHERE r.seq>f.read_message_seq AND r.sender_user_id<>$1) AS unread
				FROM space_messages r WHERE r.thread_root_id=m.id) t ON t.n>0
			WHERE f.user_id=$1 ORDER BY t.last DESC LIMIT $3`, userID, spaceID, limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			var item SpaceThread
			var raw []byte
			var files string
			var last time.Time
			if err := rows.Scan(&item.Root.Seq, &item.Root.ID, &item.Root.SpaceID, &item.Root.ConversationID, &item.Root.SenderUserID, &item.Root.SenderName, &item.Root.SenderAvatarVersion, &raw, &files, &item.Root.EditedAt, &item.Root.CreatedAt, &item.Root.ReplyToMessageID, &item.Root.ThreadRootID, &item.ReplyCount, &last, &item.UnreadCount); err != nil {
				rows.Close()
				return err
			}
			if err := decodeScannedSpaceMessage(&item.Root, raw, files); err != nil {
				rows.Close()
				return err
			}
			item.LastReplyAt = last
			item.Root.ReplyCount, item.Root.LastReplyAt = item.ReplyCount, &last
			out = append(out, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		readable := map[string]bool{}
		kept := out[:0]
		for _, item := range out {
			allowed, seen := readable[item.Root.ConversationID]
			if !seen {
				allowed = threadAccessTx(ctx, tx, userID, spaceID, item.Root.ConversationID) == nil
				readable[item.Root.ConversationID] = allowed
			}
			if allowed {
				kept = append(kept, item)
			}
		}
		out = kept
		for index := range out {
			if err := loadSpaceMessageReferencesTx(ctx, tx, &out[index].Root, userID); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}
