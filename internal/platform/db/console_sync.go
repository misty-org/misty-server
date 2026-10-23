package db

import (
	"context"
	"database/sql"
	"time"
)

// ConsoleSyncVault is one account's encrypted sync vault with its devices and
// per-device-sign-in workspaces. Content is endpoint-encrypted; only counts
// and sequence numbers are visible.
type ConsoleSyncVault struct {
	VaultID        string
	Email          string
	Devices        int
	RevokedDevices int
	Workspaces     int
	HeadSequence   int64
	KeyEpoch       int64
	ActiveDevice   string
	ActiveSeenAt   *time.Time
	CreatedAt      time.Time
}

type ConsoleSyncTotals struct {
	Vaults        int
	Devices       int
	Workspaces    int
	ActiveLastDay int
}

// ConsoleSyncVaults lists vaults, most recently active first.
func (db *Database) ConsoleSyncVaults(ctx context.Context, limit int) ([]ConsoleSyncVault, ConsoleSyncTotals, error) {
	var vaults []ConsoleSyncVault
	var totals ConsoleSyncTotals
	err := db.consoleTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `
			SELECT (SELECT count(*) FROM browser_sync_vaults),
			       (SELECT count(*) FROM browser_sync_devices WHERE revoked_at IS NULL),
			       (SELECT count(*) FROM browser_sync_workspaces WHERE workspace_id <> vault_id),
			       (SELECT count(*) FROM browser_sync_vaults WHERE active_seen_at > now() - interval '1 day')`,
		).Scan(&totals.Vaults, &totals.Devices, &totals.Workspaces, &totals.ActiveLastDay); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT v.vault_id::text, COALESCE(u.email, v.user_id),
			       (SELECT count(*) FROM browser_sync_devices d WHERE d.vault_id = v.vault_id AND d.revoked_at IS NULL),
			       (SELECT count(*) FROM browser_sync_devices d WHERE d.vault_id = v.vault_id AND d.revoked_at IS NOT NULL),
			       (SELECT count(*) FROM browser_sync_workspaces w WHERE w.vault_id = v.vault_id AND w.workspace_id <> v.vault_id),
			       v.head_sequence, v.key_epoch,
			       COALESCE((SELECT NULLIF(d.display_name, '') FROM browser_sync_devices d
			                 WHERE d.vault_id = v.vault_id AND d.device_id = v.active_device_id), ''),
			       v.active_seen_at, v.created_at
			FROM browser_sync_vaults v
			LEFT JOIN users u ON u.id = v.user_id
			ORDER BY v.active_seen_at DESC NULLS LAST, v.created_at DESC
			LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v ConsoleSyncVault
			if err := rows.Scan(&v.VaultID, &v.Email, &v.Devices, &v.RevokedDevices, &v.Workspaces,
				&v.HeadSequence, &v.KeyEpoch, &v.ActiveDevice, &v.ActiveSeenAt, &v.CreatedAt); err != nil {
				return err
			}
			vaults = append(vaults, v)
		}
		return rows.Err()
	})
	return vaults, totals, err
}
