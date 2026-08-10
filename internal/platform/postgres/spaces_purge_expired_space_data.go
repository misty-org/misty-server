package db

import (
	"context"
	"database/sql"
)

func (db *Database) PurgeExpiredSpaceData(ctx context.Context) (int64, error) {
	var purged int64
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		for _, query := range []string{
			`DELETE FROM space_messages WHERE expires_at<=NOW()`,
			`DELETE FROM space_invitations WHERE expires_at<=NOW()`,
			`DELETE FROM realtime_tickets WHERE expires_at<=NOW() OR consumed_at IS NOT NULL`,
			`DELETE FROM space_resolve_tickets WHERE expires_at<=NOW() OR consumed_at IS NOT NULL`,
			`DELETE FROM space_events WHERE created_at<=NOW()-INTERVAL '7 days'`,
		} {
			result, err := tx.ExecContext(ctx, query)
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
