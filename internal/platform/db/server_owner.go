package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/misty-org/misty-server/internal/platform/config"
)

// The server's owner is the account MISTY_OWNER_EMAIL names, never whoever
// registered first. It must sign in with Google, which proves the address: a
// server without email can't confirm a password account's, so a password
// account can't be the owner, and that email can't make one (auth.go).
// Without MISTY_OWNER_EMAIL the server has no owner.

// ServerOwnerEmail is MISTY_OWNER_EMAIL, normalized, or "" when unset.
func ServerOwnerEmail() string {
	return normalizeEmail(config.Getenv("MISTY_OWNER_EMAIL"))
}

// IsServerOwnerEmail reports whether email is the one MISTY_OWNER_EMAIL names.
func IsServerOwnerEmail(email string) bool {
	owner := ServerOwnerEmail()
	return owner != "" && normalizeEmail(email) == owner
}

// IsServerOwnerAccount reports whether an account with this email and sign-in
// provider is the server's owner.
func IsServerOwnerAccount(email, provider string) bool {
	return provider == "google" && IsServerOwnerEmail(email)
}

// IsServerOwner reports whether the account is the server's owner.
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
