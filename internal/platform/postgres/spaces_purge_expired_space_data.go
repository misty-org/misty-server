package db

import (
	"context"
	"database/sql"
)

// PurgeExpiredSpaceData deletes at most limit expired rows per table, so a
// retention backlog is drained in bounded transactions rather than one sweep.
func (db *Database) PurgeExpiredSpaceData(ctx context.Context, limit int) (int64, error) {
	if limit < 1 || limit > 5000 {
		limit = 1000
	}
	var purged int64
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		for _, expired := range []struct{ table, condition string }{
			{"space_messages", "expires_at<=NOW()"},
			{"space_invitations", "expires_at<=NOW()"},
			{"realtime_tickets", "expires_at<=NOW() OR consumed_at IS NOT NULL"},
			{"space_resolve_tickets", "expires_at<=NOW() OR consumed_at IS NOT NULL"},
			{"space_events", "created_at<=NOW()-INTERVAL '7 days'"},
		} {
			result, err := tx.ExecContext(ctx, `DELETE FROM `+expired.table+` WHERE ctid IN (SELECT ctid FROM `+expired.table+` WHERE `+expired.condition+` LIMIT $1)`, limit)
			if err != nil {
				return err
			}
			n, _ := result.RowsAffected()
			purged += n
		}
		return nil
	})
	return purged, err
}
