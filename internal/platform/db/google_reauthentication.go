package db

import (
	"context"
	"database/sql"
	"errors"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"golang.org/x/crypto/bcrypt"
)

func (db *Database) CreateGoogleReauthenticationToken(ctx context.Context, userID, hash string) error {
	return db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM google_reauthentication_tokens WHERE expires_at<=NOW()`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO google_reauthentication_tokens(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '5 minutes')`, hash, userID)
		return err
	})
}

// The proof is single-use, bound to the authenticated account, and issued only
// after fresh Google authentication. It cannot be used as a login password.
func (db *Database) VerifyAccountReauthentication(ctx context.Context, userID, password, token string) (bool, error) {
	user, err := db.GetUserByID(userID)
	if err != nil || user == nil {
		return false, err
	}
	if user.Provider == "misty" {
		return db.VerifyUserPassword(ctx, userID, password)
	}
	if user.Provider != "google" || token == "" || len(token) > 256 {
		return false, nil
	}
	var valid bool
	err = db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM google_reauthentication_tokens WHERE token_hash=$1 AND user_id=$2 AND expires_at>NOW()`, security.HashToken(token), userID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		valid = count == 1
		return err
	})
	return valid, err
}

func (db *Database) VerifyUserPassword(ctx context.Context, userID, password string) (bool, error) {
	if userID == "" || password == "" || len(password) > 1024 {
		return false, nil
	}
	var hash string
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id=$1 AND provider='misty' AND lifecycle_state='active'`, userID).Scan(&hash)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil, nil
}
