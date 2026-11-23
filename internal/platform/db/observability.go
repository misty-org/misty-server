package db

import (
	"context"
	"database/sql"
)

func (db *Database) PendingLibraryJobs(ctx context.Context) (map[string]int64, error) {
	counts := map[string]int64{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
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

func (db *Database) ActiveUploadReservations(ctx context.Context) (int64, error) {
	var count int64
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM space_upload_reservations WHERE state='active'`).Scan(&count)
	})
	return count, err
}

func (db *Database) ActiveUserCount(ctx context.Context) (int64, error) {
	var count int64
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT COUNT(DISTINCT user_id) FROM sessions WHERE expires_at>NOW()`).Scan(&count)
	})
	return count, err
}
