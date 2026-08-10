package db

import (
	"context"
	"database/sql"
)

// Queue depths and reservation counts, for metrics.
//
// These are leading indicators: a rising queue tells you the server is falling
// behind before CPU saturates, whereas CPU tells you only once it already has.

// PendingLibraryJobs counts queued or leased background jobs by kind.
func (db *Database) PendingLibraryJobs(ctx context.Context) (map[string]int64, error) {
	counts := map[string]int64{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT job_kind,COUNT(*) FROM library_processing_jobs
			 WHERE state IN ('queued','leased','running') GROUP BY job_kind`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var kind string
			var count int64
			if err := rows.Scan(&kind, &count); err != nil {
				return err
			}
			counts[kind] = count
		}
		return rows.Err()
	})
	return counts, err
}

// ActiveUploadReservations counts uploads holding quota but not yet finalized.
// A number that climbs and never falls means clients are abandoning uploads and
// the cleanup worker is not keeping up.
func (db *Database) ActiveUploadReservations(ctx context.Context) (int64, error) {
	var count int64
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM space_upload_reservations WHERE state='active'`).Scan(&count)
	})
	return count, err
}

// ActiveUserCount counts users with a session that has not expired, which is a
// far better answer to "how many people are using this" than an edge proxy's
// IP-and-user-agent heuristic.
func (db *Database) ActiveUserCount(ctx context.Context) (int64, error) {
	var count int64
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT COUNT(DISTINCT user_id) FROM sessions WHERE expires_at>NOW()`).Scan(&count)
	})
	return count, err
}
