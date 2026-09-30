package db

import (
	"context"
	"database/sql"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"unicode/utf8"
)

var ErrLibraryPasswordNotSet = errors.New("set a library password before unlocking this collection")
var ErrLibraryPasswordAlreadySet = errors.New("a library password is already set; unlock with your existing library password")
var ErrLibraryPasswordInvalid = errors.New("library password must contain at least 8 characters and fit within 72 UTF-8 bytes")

func (db *Database) LibraryPasswordConfigured(ctx context.Context, userID string) (bool, error) {
	var configured bool
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM library_lock_credentials WHERE user_id=$1)`, userID).Scan(&configured)
	})
	return configured, err
}

// Initial setup is atomic and cannot replace an existing credential. The lock
// belongs to the server account and is independent of either sign-in provider.
func (db *Database) SetInitialLibraryPassword(ctx context.Context, userID, password string) error {
	if utf8.RuneCountInString(password) < 8 || len(password) > 72 {
		return ErrLibraryPasswordInvalid
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO library_lock_credentials(user_id,password_hash)
   SELECT id,$2 FROM users WHERE id=$1 AND lifecycle_state='active' ON CONFLICT(user_id) DO NOTHING`, userID, string(hash))
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrLibraryPasswordAlreadySet
		}
		return nil
	})
}

func (db *Database) VerifyLibraryPassword(ctx context.Context, userID, password string) (bool, error) {
	var hash string
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT c.password_hash FROM library_lock_credentials c JOIN users u ON u.id=c.user_id WHERE c.user_id=$1 AND u.lifecycle_state='active'`, userID).Scan(&hash)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrLibraryPasswordNotSet
	}
	if err != nil {
		return false, err
	}
	if password == "" || len(password) > 72 {
		return false, nil
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil, nil
}
