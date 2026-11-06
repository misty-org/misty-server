package db

import (
	"context"
	"database/sql"
	"time"
)

// ConsoleSyncVault is one account's encrypted vault. Content is end-to-end encrypted: only counts
// and sizes are visible.
type ConsoleSyncVault struct {
	VaultID         string
	Email           string
	Sessions        int
	RevokedSessions int
	Records         int64
	Bytes           int64
	HeadSequence    int64
	LastSeenAt      *time.Time
	CreatedAt       time.Time
}

type ConsoleSyncTotals struct {
	Vaults        int
	Sessions      int
	Records       int64
	ActiveLastDay int
}

// ConsoleSyncVaults lists vaults, most recently active first.
func (db *Database) ConsoleSyncVaults(ctx context.Context, limit int) ([]ConsoleSyncVault, ConsoleSyncTotals, error) {
	var vaults []ConsoleSyncVault
	var totals ConsoleSyncTotals
	err := db.consoleTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `
			SELECT (SELECT count(*) FROM sync_vaults),
			       (SELECT count(*) FROM sync_sessions WHERE revoked_at IS NULL),
			       (SELECT count(*) FROM sync_records WHERE NOT deleted),
			       (SELECT count(DISTINCT vault_id) FROM sync_sessions WHERE last_seen_at > now() - interval '1 day')`,
		).Scan(&totals.Vaults, &totals.Sessions, &totals.Records, &totals.ActiveLastDay); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT v.vault_id::text, COALESCE(u.email, v.user_id),
			       (SELECT count(*) FROM sync_sessions s WHERE s.vault_id = v.vault_id AND s.revoked_at IS NULL),
			       (SELECT count(*) FROM sync_sessions s WHERE s.vault_id = v.vault_id AND s.revoked_at IS NOT NULL),
			       (SELECT count(*) FROM sync_records r WHERE r.vault_id = v.vault_id AND NOT r.deleted),
			       COALESCE((SELECT sum(octet_length(r.sealed)) FROM sync_records r WHERE r.vault_id = v.vault_id), 0),
			       v.head_sequence,
			       (SELECT max(s.last_seen_at) FROM sync_sessions s WHERE s.vault_id = v.vault_id) AS seen,
			       v.created_at
			FROM sync_vaults v
			LEFT JOIN users u ON u.id = v.user_id
			ORDER BY seen DESC NULLS LAST, v.created_at DESC
			LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v ConsoleSyncVault
			if err := rows.Scan(&v.VaultID, &v.Email, &v.Sessions, &v.RevokedSessions, &v.Records,
				&v.Bytes, &v.HeadSequence, &v.LastSeenAt, &v.CreatedAt); err != nil {
				return err
			}
			vaults = append(vaults, v)
		}
		return rows.Err()
	})
	return vaults, totals, err
}
