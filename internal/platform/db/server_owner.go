package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/misty-org/misty-server/internal/platform/config"
)

func ServerOwnerEmail() string {
	return normalizeEmail(config.Getenv("MISTY_OWNER_EMAIL"))
}

func IsServerOwnerEmail(email string) bool {
	owner := ServerOwnerEmail()
	return owner != "" && normalizeEmail(email) == owner
}

func IsServerOwnerAccount(email, provider string) bool {
	return provider == "google" && IsServerOwnerEmail(email)
}

func (db *Database) IsServerOwner(ctx context.Context, userID string) (bool, error) {
	if ServerOwnerEmail() == "" {
		return false, nil
	}
	var email, provider string
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT email,provider FROM users WHERE id=$1`, userID).Scan(&email, &provider)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return IsServerOwnerAccount(email, provider), nil
}
