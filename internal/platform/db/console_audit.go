package db

import (
	"context"
	"database/sql"
	"time"
)

type ConsoleAuditEntry struct {
	Action    string
	Target    string
	Detail    string
	CreatedAt time.Time
}

// ConsoleRecordAudit appends an operator action. Callers must never pass
// passwords or tokens in target or detail.
func (db *Database) ConsoleRecordAudit(ctx context.Context, action, target, detail string) error {
	return db.consoleTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO console_audit_log (action, target, detail) VALUES ($1, left($2, 200), left($3, 500))`,
			action, target, detail)
		return err
	})
}

func (db *Database) ConsoleRecentAudit(ctx context.Context, limit int) ([]ConsoleAuditEntry, error) {
	var entries []ConsoleAuditEntry
	err := db.consoleTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT action, target, detail, created_at FROM console_audit_log
			ORDER BY created_at DESC LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e ConsoleAuditEntry
			if err := rows.Scan(&e.Action, &e.Target, &e.Detail, &e.CreatedAt); err != nil {
				return err
			}
			entries = append(entries, e)
		}
		return rows.Err()
	})
	return entries, err
}

// ConsoleOverviewCounts returns the number of active accounts.
func (db *Database) ConsoleOverviewCounts(ctx context.Context) (accounts int, err error) {
	err = db.consoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE lifecycle_state = 'active'`).Scan(&accounts)
	})
	return accounts, err
}
