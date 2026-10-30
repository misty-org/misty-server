package browsersync

import (
	"context"
	"database/sql"

	"github.com/lib/pq"
)

func renewSyncInstance(ctx context.Context, tx *sql.Tx, instanceID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO browser_sync_instances(instance_id,expires_at) VALUES($1,clock_timestamp()+$2::interval)
 ON CONFLICT(instance_id) DO UPDATE SET expires_at=EXCLUDED.expires_at`, instanceID, SyncInstanceLease.String())
	return err
}

// RenewBrowserSyncInstance extends this process's lease and removes the
// connections of processes whose lease lapsed, publishing presence for each
// affected vault. It is one bounded pass per process, independent of how many
// devices are connected. Legacy rows expire in bounded batches.
func (db *Store) RenewBrowserSyncInstance(ctx context.Context, instanceID string) (int, error) {
	if !validSyncID(instanceID) {
		return 0, ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err = renewSyncInstance(ctx, tx, instanceID); err != nil {
		return 0, err
	}
	var dead []string
	rows, err := tx.QueryContext(ctx, `DELETE FROM browser_sync_instances WHERE instance_id IN (
 SELECT instance_id FROM browser_sync_instances WHERE expires_at<=clock_timestamp() FOR UPDATE SKIP LOCKED LIMIT 100)
 RETURNING instance_id`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		dead = append(dead, id)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	removed := 0
	if len(dead) > 0 {
		if removed, err = sweepSyncConnections(ctx, tx, `DELETE FROM browser_sync_connections c WHERE c.instance_id=ANY($1::uuid[])
 RETURNING c.vault_id,c.device_id,c.last_seen_at`, pq.Array(dead)); err != nil {
			return 0, err
		}
	}
	legacy, err := sweepSyncConnections(ctx, tx, `DELETE FROM browser_sync_connections c WHERE c.connection_id IN (
 SELECT connection_id FROM browser_sync_connections WHERE instance_id IS NULL AND expires_at<=clock_timestamp() LIMIT 1000)
 RETURNING c.vault_id,c.device_id,c.last_seen_at`)
	if err != nil {
		return 0, err
	}
	return removed + legacy, tx.Commit()
}

// ReleaseBrowserSyncInstance ends this process's lease on shutdown so its
// devices go offline at once rather than when the lease lapses.
func (db *Store) ReleaseBrowserSyncInstance(ctx context.Context, instanceID string) error {
	if !validSyncID(instanceID) {
		return ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM browser_sync_instances WHERE instance_id=$1`, instanceID); err != nil {
		return err
	}
	if _, err = sweepSyncConnections(ctx, tx, `DELETE FROM browser_sync_connections c WHERE c.instance_id=$1::uuid
 RETURNING c.vault_id,c.device_id,c.last_seen_at`, instanceID); err != nil {
		return err
	}
	return tx.Commit()
}

// sweepSyncConnections runs a DELETE ... RETURNING vault_id,device_id,last_seen_at,
// keeps each device's latest last-seen time and publishes presence once per vault.
func sweepSyncConnections(ctx context.Context, tx *sql.Tx, deletion string, args ...any) (int, error) {
	rows, err := tx.QueryContext(ctx, `WITH removed AS (`+deletion+`),
 seen AS (UPDATE browser_sync_devices d SET last_seen_at=GREATEST(d.last_seen_at,r.seen)
  FROM (SELECT vault_id,device_id,max(last_seen_at) AS seen FROM removed GROUP BY 1,2) r
  WHERE d.vault_id=r.vault_id AND d.device_id=r.device_id RETURNING d.vault_id)
 SELECT w.user_id,w.vault_id,(SELECT count(*) FROM removed) FROM browser_sync_vaults w WHERE w.vault_id IN (SELECT vault_id FROM removed)`, args...)
	if err != nil {
		return 0, err
	}
	type vault struct{ user, id string }
	var vaults []vault
	removed := 0
	for rows.Next() {
		var v vault
		if err = rows.Scan(&v.user, &v.id, &removed); err != nil {
			rows.Close()
			return 0, err
		}
		vaults = append(vaults, v)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	for _, v := range vaults {
		if err = notifySync(ctx, tx, v.user, v.id, "browser-presence"); err != nil {
			return 0, err
		}
	}
	return removed, nil
}
