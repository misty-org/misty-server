package db

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"
)

var ErrAuthHandoffTokenInvalid = errors.New("auth handoff token is invalid or expired")

// AuthHandoffTTL is deliberately short: the desktop app mints a token and opens
// the browser with it immediately, so anything longer only widens the window in
// which a leaked URL is useful.
const AuthHandoffTTL = 60 * time.Second

// AuthHandoffSessionTTL is the lifetime of the browser session minted from a
// handoff. It is much shorter than SessionTTL because the user never typed a
// password in that browser — they only clicked a button in the desktop app.
const AuthHandoffSessionTTL = 12 * time.Hour

func (db *Database) CreateAuthHandoffToken(userID, hashedToken, redirectPath string, expiresAt time.Time) error {
	err := db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(
			context.Background(),
			`INSERT INTO auth_handoff_tokens (hashed_token, user_id, redirect_path, expires_at)
			 SELECT $1,$2,$3,$4 FROM users
			 WHERE id=$2 AND lifecycle_state='active'`,
			hashedToken, userID, redirectPath, expiresAt,
		)
		return err
	})
	if err != nil {
		log.Println("Failed to create auth handoff token:", err)
	}
	return err
}

// ConsumeAuthHandoffToken deletes the token and returns its owner in a single
// transaction, so a replayed URL cannot mint a second session. Compare
// ValidatePasswordResetToken, which deliberately leaves its token in place.
func (db *Database) ConsumeAuthHandoffToken(hashedToken string, now time.Time) (userID string, redirectPath string, err error) {
	var invalidTokenErr error
	err = db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var expiresAt time.Time
		scanErr := tx.QueryRowContext(
			context.Background(),
			`SELECT user_id, redirect_path, expires_at
			 FROM auth_handoff_tokens
			 WHERE hashed_token = $1
			 FOR UPDATE`,
			hashedToken,
		).Scan(&userID, &redirectPath, &expiresAt)
		if scanErr != nil {
			if errors.Is(scanErr, sql.ErrNoRows) {
				invalidTokenErr = ErrAuthHandoffTokenInvalid
				return nil
			}
			log.Println("Failed to fetch auth handoff token:", scanErr)
			return scanErr
		}

		if _, delErr := tx.ExecContext(
			context.Background(),
			`DELETE FROM auth_handoff_tokens WHERE hashed_token = $1`,
			hashedToken,
		); delErr != nil {
			log.Println("Failed to delete auth handoff token:", delErr)
			return delErr
		}

		if !expiresAt.After(now) {
			invalidTokenErr = ErrAuthHandoffTokenInvalid
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	if invalidTokenErr != nil {
		return "", "", invalidTokenErr
	}
	return userID, redirectPath, nil
}

// DeleteExpiredAuthHandoffTokens keeps the table from accumulating rows that can
// never be redeemed. Consumption already deletes on the happy path; this only
// reaps tokens that were minted and never used.
func (db *Database) DeleteExpiredAuthHandoffTokens(now time.Time) error {
	err := db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(
			context.Background(),
			`DELETE FROM auth_handoff_tokens WHERE expires_at <= $1`,
			now,
		)
		return err
	})
	if err != nil {
		log.Println("Failed to delete expired auth handoff tokens:", err)
	}
	return err
}
