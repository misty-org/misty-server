package cloudusage

import (
	"context"
	"database/sql"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

// Commit checks only accounts whose payload grew in this transaction. Shrinks
// and deletion never need new admission, even during a billing outage. The
// trigger holds the same account lock as uploads before changing native data.
func Commit(ctx context.Context, tx *sql.Tx) error {
	var readOnly string
	if err := tx.QueryRowContext(ctx, `SHOW transaction_read_only`).Scan(&readOnly); err != nil {
		return err
	}
	if readOnly == "on" {
		return tx.Commit()
	}
	rows, err := tx.QueryContext(ctx, `DELETE FROM account_cloud_mutations WHERE transaction_id=txid_current() RETURNING user_id,added_bytes,removed_bytes`)
	if err != nil {
		return err
	}
	type change struct {
		user           string
		added, removed int64
	}
	var changes []change
	for rows.Next() {
		var c change
		if err = rows.Scan(&c.user, &c.added, &c.removed); err != nil {
			rows.Close()
			return err
		}
		changes = append(changes, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range changes {
		if c.added <= c.removed {
			continue
		}
		adapter, err := envconfig.BillingAdapter()
		if err != nil {
			return err
		}
		if !adapter.Enabled() {
			continue
		}
		if _, err = Check(ctx, tx, adapter, c.user, "storage.commit", map[string]int64{"added_bytes": c.added, "removed_bytes": c.removed}); err != nil {
			return err
		}
	}
	return tx.Commit()
}
