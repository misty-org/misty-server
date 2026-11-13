package db

import (
	"context"
	"database/sql"
	"time"
)

// Operator console queries. They run in service RLS scope because the console
// administers every account; the console process is loopback-only and never
// mounted on the public API.

type ConsoleAccount struct {
	ID        string
	Email     string
	Username  string
	State     string
	CreatedAt time.Time
	Sessions  int
}

type ConsoleAccountDetail struct {
	ConsoleAccount
	StorageBytes int64
}

func (db *Database) consoleTx(ctx context.Context, fn func(*sql.Tx) error) error {
	return db.WithRLSContext(ctx, ServiceRLSSettings(), fn)
}

const consoleAccountColumns = `
	u.id, u.email, u.username, u.lifecycle_state, u.created_at,
	(SELECT count(*) FROM sessions s WHERE s.user_id = u.id AND s.expires_at > now())
	FROM users u`

func scanConsoleAccount(row interface{ Scan(...any) error }, a *ConsoleAccount) error {
	return row.Scan(&a.ID, &a.Email, &a.Username, &a.State, &a.CreatedAt, &a.Sessions)
}

func (db *Database) ConsoleAccountDetail(ctx context.Context, userID string) (ConsoleAccountDetail, error) {
	var d ConsoleAccountDetail
	err := db.consoleTx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `SELECT `+consoleAccountColumns+` WHERE u.id = $1`, userID)
		if err := scanConsoleAccount(row, &d.ConsoleAccount); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `
			SELECT COALESCE((SELECT used_bytes FROM owner_storage_usage WHERE owner_user_id = $1), 0)`, userID,
		).Scan(&d.StorageBytes)
	})
	return d, err
}

// ConsoleRevokeSessions signs the account out everywhere.
func (db *Database) ConsoleRevokeSessions(ctx context.Context, userID string) (int64, error) {
	var revoked int64
	err := db.consoleTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
		if err != nil {
			return err
		}
		revoked, err = result.RowsAffected()
		return err
	})
	return revoked, err
}
