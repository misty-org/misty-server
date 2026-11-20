package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// NextWorkerDelay uses database time so API-host clock skew cannot strand a
// scheduled retry. NULL means no pending work and therefore no timer or scan.
func (db *Database) NextWorkerDelay(ctx context.Context, queue string) (time.Duration, bool, error) {
	var deadline string
	switch queue {
	case "abuse-retention":
		deadline = `SELECT min(blocked_until)+interval '1 day' FROM abuse_blocks`
	case "note-control":
		deadline = `SELECT min(o.next_attempt_at) FROM space_note_control_outbox o WHERE ` + noteControlHead
	case "code-session-control":
		deadline = `SELECT min(next_attempt_at) FROM space_code_session_control_outbox WHERE delivered_at IS NULL`
	case "account-deletion":
		deadline = accountDeletionWorkerDeadline
	case "rendition-reservations":
		deadline = renditionReservationWorkerDeadline
	default:
		return 0, false, errors.New("unknown worker queue")
	}
	var seconds sql.NullFloat64
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXTRACT(EPOCH FROM ((`+deadline+`)-clock_timestamp()))`).Scan(&seconds)
	})
	if err != nil || !seconds.Valid {
		return 0, false, err
	}
	// Avoid duration overflow for rows deliberately scheduled far in the future.
	delay := time.Duration(max(0, min(seconds.Float64, 365*24*3600)) * float64(time.Second))
	return delay, true, nil
}
