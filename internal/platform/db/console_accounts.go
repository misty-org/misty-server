package db

import (
	"context"
	"database/sql"
	"errors"
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
	// Unconfirmed is a password account whose email was never proven, so its email may be
	// released (ConsoleReleaseEmail).
	Unconfirmed bool
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
	(SELECT count(*) FROM sessions s WHERE s.user_id = u.id AND s.expires_at > now()),
	(u.provider = 'misty' AND u.email_verified_at IS NULL AND u.email NOT LIKE 'released-%@invalid')
	FROM users u`

func scanConsoleAccount(row interface{ Scan(...any) error }, a *ConsoleAccount) error {
	return row.Scan(&a.ID, &a.Email, &a.Username, &a.State, &a.CreatedAt, &a.Sessions, &a.Unconfirmed)
}

// ErrEmailConfirmed means the account's email was proven, so it isn't the console's to release.
var ErrEmailConfirmed = errors.New("this account's email is confirmed, so it can't be released")

// ConsoleReleaseEmail frees the email of a password account that never proved it, so the
// address's real owner can make an account with it (on a server that sends no email, anyone
// can sign up with any address). The account and everything in it stay, under a placeholder
// address nobody can sign in with: its password, sessions, reset grants and setup keys go, so
// whoever made it can't reach it again. The operator may close it afterwards. It returns the
// email released.
func (db *Database) ConsoleReleaseEmail(ctx context.Context, userID string) (string, error) {
	var email string
	err := db.consoleTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT email FROM users WHERE id=$1 AND provider='misty'
   AND email_verified_at IS NULL AND email NOT LIKE 'released-%@invalid' FOR UPDATE`, userID).Scan(&email)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrEmailConfirmed
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET email='released-'||id||'@invalid', password_hash='' WHERE id=$1`, userID); err != nil {
			return err
		}
		for _, statement := range []string{
			`DELETE FROM sessions WHERE user_id=$1`,
			`DELETE FROM password_reset_tokens WHERE user_id=$1`,
			`DELETE FROM account_setup_keys WHERE user_id=$1`,
		} {
			if _, err := tx.ExecContext(ctx, statement, userID); err != nil {
				return err
			}
		}
		return nil
	})
	return email, err
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
