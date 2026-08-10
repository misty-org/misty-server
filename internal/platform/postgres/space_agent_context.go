package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// SpaceContextRevision is a cheap change token for everything the agent's Space
// context is built from. It lets the send path skip rebuilding context when
// nothing the agent can see has changed since the last turn.
//
// It must cover every source PersonalAgentSpaceContextForConversation reads, or
// a future agent would answer from a stale view. Owner/Member permissions are
// fixed, so caller identity and current membership cover authorization changes.
//
// It is deliberately one round trip. Rebuilding context costs several queries
// plus prompt tokens on every turn, so a single query that usually says
// "unchanged" pays for itself immediately.
func (db *Database) SpaceContextRevision(ctx context.Context, userID, spaceID string) (string, error) {
	var (
		spaceUpdatedAt   time.Time
		messageSeq       sql.NullInt64
		messageEditedAt  sql.NullTime
		taskUpdatedAt    sql.NullTime
		libraryUpdatedAt sql.NullTime
		memberCount      int64
		memberJoinedAt   sql.NullTime
	)
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `
			SELECT
				(SELECT updated_at FROM spaces WHERE id=$1),
				(SELECT MAX(seq) FROM space_messages WHERE space_id=$1),
				(SELECT MAX(edited_at) FROM space_messages WHERE space_id=$1),
				(SELECT MAX(updated_at) FROM space_tasks WHERE space_id=$1),
				(SELECT MAX(updated_at) FROM space_library_items WHERE space_id=$1),
				(SELECT COUNT(*) FROM space_members WHERE space_id=$1),
				(SELECT MAX(joined_at) FROM space_members WHERE space_id=$1)
		`, spaceID).Scan(
			&spaceUpdatedAt,
			&messageSeq,
			&messageEditedAt,
			&taskUpdatedAt,
			&libraryUpdatedAt,
			&memberCount,
			&memberJoinedAt,
		)
	})
	if err != nil {
		return "", err
	}

	// space_messages has no updated_at, so MAX(seq) covers inserts and
	// MAX(edited_at) covers edits. space_members has no updated_at either, so
	// COUNT plus MAX(joined_at) covers joins and removals. A same-second
	// remove-and-rejoin of two different members is the one case this cannot
	// distinguish; it would leave the previous turn's member list in place, which
	// is a stale name rather than a permission leak, since every section still
	// re-checks its own permission when it is rebuilt.
	digest := sha256.Sum256([]byte(fmt.Sprintf(
		"space=%s|user=%s|space_at=%d|msg_seq=%d|msg_edit=%d|task_at=%d|lib_at=%d|members=%d|joined=%d",
		spaceID,
		userID,
		spaceUpdatedAt.UnixNano(),
		messageSeq.Int64,
		nullTimeNanos(messageEditedAt),
		nullTimeNanos(taskUpdatedAt),
		nullTimeNanos(libraryUpdatedAt),
		memberCount,
		nullTimeNanos(memberJoinedAt),
	)))
	return hex.EncodeToString(digest[:]), nil
}

func nullTimeNanos(value sql.NullTime) int64 {
	if !value.Valid {
		return 0
	}
	return value.Time.UnixNano()
}
