package db

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var ErrPasswordResetTokenInvalid = errors.New("password reset token is invalid or expired")

func (db *Database) UpsertPasswordResetToken(userID, hashedToken string, expiresAt time.Time) error {
	_, err := db.Conn.Exec(
		`INSERT INTO password_reset_tokens (user_id, hashed_token, expires_at)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (user_id)
		 DO UPDATE SET hashed_token = EXCLUDED.hashed_token, expires_at = EXCLUDED.expires_at, created_at = NOW()`,
		userID, hashedToken, expiresAt,
	)
	if err != nil {
		log.Println("Failed to upsert password reset token:", err)
		return err
	}

	return nil
}

func (db *Database) ValidatePasswordResetToken(hashedToken string, now time.Time) error {
	var userID string
	var expiresAt time.Time

	err := db.Conn.QueryRow(
		`SELECT user_id, expires_at
		 FROM password_reset_tokens
		 WHERE hashed_token = $1`,
		hashedToken,
	).Scan(&userID, &expiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPasswordResetTokenInvalid
		}
		log.Println("Failed to validate password reset token:", err)
		return err
	}

	if !expiresAt.After(now) {
		if _, err := db.Conn.Exec(`DELETE FROM password_reset_tokens WHERE user_id = $1`, userID); err != nil {
			log.Println("Failed to delete expired password reset token:", err)
			return err
		}
		return ErrPasswordResetTokenInvalid
	}

	return nil
}

func (db *Database) ResetPasswordWithToken(hashedToken, newPassword string, now time.Time) error {
	tx, err := db.Conn.BeginTx(context.Background(), &sql.TxOptions{})
	if err != nil {
		log.Println("Failed to begin password reset transaction:", err)
		return err
	}

	committed := false
	defer func() {
		if !committed {
			rollbackTx(tx)
		}
	}()

	var userID string
	var expiresAt time.Time
	err = tx.QueryRow(
		`SELECT user_id, expires_at
		 FROM password_reset_tokens
		 WHERE hashed_token = $1
		 FOR UPDATE`,
		hashedToken,
	).Scan(&userID, &expiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPasswordResetTokenInvalid
		}
		log.Println("Failed to fetch password reset token:", err)
		return err
	}

	if !expiresAt.After(now) {
		if _, err := tx.Exec(`DELETE FROM password_reset_tokens WHERE user_id = $1`, userID); err != nil {
			log.Println("Failed to delete expired password reset tokens:", err)
			return err
		}
		if err := tx.Commit(); err != nil {
			log.Println("Failed to commit expired password reset cleanup:", err)
			return err
		}
		committed = true
		return ErrPasswordResetTokenInvalid
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	result, err := tx.Exec(`UPDATE users SET password_hash = $1 WHERE id = $2`, string(passwordHash), userID)
	if err != nil {
		log.Println("Failed to update user password:", err)
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Println("Failed to read password update result:", err)
		return err
	}
	if rowsAffected != 1 {
		return ErrPasswordResetTokenInvalid
	}

	if _, err := tx.Exec(`DELETE FROM password_reset_tokens WHERE user_id = $1`, userID); err != nil {
		log.Println("Failed to delete password reset tokens:", err)
		return err
	}

	if err := tx.Commit(); err != nil {
		log.Println("Failed to commit password reset transaction:", err)
		return err
	}

	committed = true
	return nil
}

func rollbackTx(tx *sql.Tx) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		log.Println("Failed to rollback transaction:", err)
	}
}
