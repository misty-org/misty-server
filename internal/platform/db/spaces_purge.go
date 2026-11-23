package db

import (
	"context"
	"database/sql"
)

func (db *Database) PurgeExpiredSpaceData(ctx context.Context, limit int) (int64, error) {
	if limit < 1 || limit > 5000 {
		limit = 1000
	}
	var purged int64
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		for _, expired := range []struct{ table, condition string }{
			{"space_messages", "expires_at<=NOW()"},
			{"space_invitations", "expires_at<=NOW()"},
			{"realtime_tickets", "expires_at<=NOW() OR consumed_at IS NOT NULL"},
			{"space_resolve_tickets", "expires_at<=NOW() OR consumed_at IS NOT NULL"},
		} {
			result, err := tx.ExecContext(ctx, `DELETE FROM `+expired.table+` WHERE ctid IN (SELECT ctid FROM `+expired.table+` WHERE `+expired.condition+` LIMIT $1)`, limit)
			if err != nil {
				return err
			}
			n, _ := result.RowsAffected()
			purged += n
		}
		var events int64
		if err := tx.QueryRowContext(ctx, `WITH gone AS (
				DELETE FROM space_events WHERE ctid IN (SELECT ctid FROM space_events WHERE created_at<=NOW()-INTERVAL '7 days' LIMIT $1)
				RETURNING id,space_id
			), marked AS (
				INSERT INTO space_event_horizon(space_id,purged_through)
				SELECT gone.space_id,max(gone.id) FROM gone JOIN spaces s ON s.id=gone.space_id GROUP BY gone.space_id
				ON CONFLICT(space_id) DO UPDATE SET purged_through=GREATEST(space_event_horizon.purged_through,excluded.purged_through),updated_at=NOW()
			)
			SELECT count(*) FROM gone`, limit).Scan(&events); err != nil {
			return err
		}
		purged += events
		return nil
	})
	return purged, err
}
