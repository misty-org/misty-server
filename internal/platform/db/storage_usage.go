package db

import (
	"context"
	"database/sql"
)

// storageUsageState is what an account and a Space hold in cloud storage.
// Storage is not limited; the counters are informational.
type storageUsageState struct {
	Personal StorageUsageDimension
	Space    StorageUsageDimension
}

// lockAccountStorageTx serializes one account's storage writes.
func lockAccountStorageTx(ctx context.Context, tx *sql.Tx, user string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "storage-personal:"+user)
	return err
}

// accountStorageTx counts an account's retained payloads across all its
// Spaces. Content stores use per-Space RLS contexts, so the inventory reads
// as the service. Encrypted browser sync data is not counted.
func accountStorageTx(ctx context.Context, tx *sql.Tx, user string) (StorageUsageDimension, error) {
	var previous sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT current_setting('app.rls_mode',true)`).Scan(&previous); err != nil {
		return StorageUsageDimension{}, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.rls_mode','service',true)`); err != nil {
		return StorageUsageDimension{}, err
	}
	defer tx.ExecContext(ctx, `SELECT set_config('app.rls_mode',$1,true)`, previous.String)
	var objects, reserved, database int64
	err := tx.QueryRowContext(ctx, `SELECT
 COALESCE((SELECT sum(logical_bytes) FROM space_storage_contributions WHERE user_id=$1 AND state IN ('active','recovery')),0),
 COALESCE((SELECT sum(reserved_bytes) FROM space_upload_reservations WHERE user_id=$1 AND state='active'),0)+COALESCE((SELECT sum(reserved_bytes) FROM space_rendition_reservations WHERE user_id=$1 AND state='active'),0),
 COALESCE((SELECT sum(octet_length(content::text)) FROM space_messages WHERE sender_user_id=$1),0)+
 COALESCE((SELECT sum(octet_length(title_projection)+octet_length(markdown_projection)+octet_length(plain_text_projection)+octet_length(shared_tags::text)) FROM space_notes WHERE creator_user_id=$1),0)`, user).Scan(&objects, &reserved, &database)
	return StorageUsageDimension{UsedBytes: objects + database, ReservedBytes: reserved}, err
}

func storageUsageStateTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, lock bool) (storageUsageState, error) {
	if lock {
		if err := lockAccountStorageTx(ctx, tx, userID); err != nil {
			return storageUsageState{}, err
		}
	}
	personal, err := accountStorageTx(ctx, tx, userID)
	if err != nil {
		return storageUsageState{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO space_storage_usage(space_id) VALUES($1) ON CONFLICT DO NOTHING`, spaceID); err != nil {
		return storageUsageState{}, err
	}
	var space StorageUsageDimension
	if err = tx.QueryRowContext(ctx, `SELECT used_bytes,reserved_bytes FROM space_storage_usage WHERE space_id=$1`, spaceID).Scan(&space.UsedBytes, &space.ReservedBytes); err != nil {
		return storageUsageState{}, err
	}
	return storageUsageState{Personal: personal, Space: space}, nil
}

// prepareStorageReservationTx locks the account and makes sure the Space's
// attribution row exists before an upload reserves bytes.
func prepareStorageReservationTx(ctx context.Context, tx *sql.Tx, userID, spaceID string) error {
	if err := lockAccountStorageTx(ctx, tx, userID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO space_storage_usage(space_id) VALUES($1) ON CONFLICT DO NOTHING`, spaceID)
	return err
}

// renditionReserveBytes is how much a rendition reserves while it renders:
// the requested ceiling, else the source size with a 1 MB floor.
func renditionReserveBytes(ctx context.Context, tx *sql.Tx, userID string, requested, source int64) (int64, error) {
	if err := lockAccountStorageTx(ctx, tx, userID); err != nil {
		return 0, err
	}
	if requested > 0 {
		return requested, nil
	}
	return max(int64(1_000_000), source), nil
}
