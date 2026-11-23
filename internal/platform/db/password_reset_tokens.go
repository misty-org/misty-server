package db

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/misty-org/misty-server/internal/accounts"
	"golang.org/x/crypto/bcrypt"
)

const PasswordResetTTL = 30 * time.Minute

var ErrPasswordResetTokenInvalid = errors.New("password reset token is invalid or expired")

func (db *Database) GrantPasswordReset(ctx context.Context, userID, hashedToken string, expiresAt time.Time) error {
	return db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO password_reset_tokens (user_id, hashed_token, expires_at)
			 VALUES ($1, $2, $3)
			 ON CONFLICT (user_id)
			 DO UPDATE SET hashed_token = EXCLUDED.hashed_token, expires_at = EXCLUDED.expires_at, created_at = NOW()`,
			userID, hashedToken, expiresAt)
		return err
	})
}

func (db *Database) ResetPassword(ctx context.Context, userID, hashedToken, newPassword, keepSessionHash string, now time.Time) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), accounts.PasswordCost)
	if err != nil {
		return err
	}
	return db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		var expiresAt time.Time
		err := tx.QueryRowContext(ctx,
			`SELECT expires_at FROM password_reset_tokens WHERE user_id=$1 AND hashed_token=$2 FOR UPDATE`,
			userID, hashedToken).Scan(&expiresAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPasswordResetTokenInvalid
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM password_reset_tokens WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if !expiresAt.After(now) {
			return ErrPasswordResetTokenInvalid
		}
		result, err := tx.ExecContext(ctx,
			`UPDATE users SET password_hash=$1 WHERE id=$2 AND provider='misty' AND lifecycle_state='active'`,
			string(hash), userID)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil {
			return err
		} else if rows != 1 {
			return ErrNoPasswordAccount
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=$1 AND token_hash<>$2`, userID, keepSessionHash)
		return err
	})
}

func rollbackTx(tx *sql.Tx) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		log.Println("Failed to rollback transaction:", err)
	}
}
