package accounts

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
)

var ErrEmailTaken = errors.New("email already registered")
var ErrProviderConflict = errors.New("this email belongs to an existing account; use its original sign-in method")

// GoogleUser accepts only a server-verified Google identity. Never link an
// existing account by email: Google's stable subject owns the identity.
func (db Store) GoogleUser(name, email, subject string) (*User, error) {
	email = NormalizeEmail(email)
	if email == "" || subject == "" {
		return nil, errors.New("Google identity is incomplete")
	}
	var id, storedEmail, lifecycle string
	err := db.Transaction(context.Background(), map[string]string{"app.rls_mode": "service"}, func(tx *sql.Tx) error {
		return tx.QueryRowContext(context.Background(), `SELECT id,email,lifecycle_state FROM users WHERE provider='google' AND provider_subject=$1`, subject).Scan(&id, &storedEmail, &lifecycle)
	})
	if err == nil {
		if lifecycle != "active" {
			return nil, ErrProviderConflict
		}
		// Keep the verified email current, while preserving the global uniqueness
		// constraint. An email move must never merge two Misty accounts.
		if storedEmail != email {
			err = db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
				_, err := tx.ExecContext(context.Background(), `UPDATE users SET email=$2 WHERE id=$1`, id, email)
				return err
			})
			if err != nil {
				return nil, ErrProviderConflict
			}
		}
		return db.GetUserByID(id)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	existing, _, err := db.GetUserByEmail(email)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrProviderConflict
	}
	username := DefaultUsernameForEmail(email)
	for attempt := 0; attempt < 3; attempt++ {
		user, err := db.createIdentity(name, username, email, "", "google", subject)
		if err == nil {
			return user, nil
		}
		if errors.Is(err, ErrUsernameTaken) {
			username = username[:min(len(username), 20)] + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
			continue
		}
		// A concurrent callback may have created this exact identity already.
		existing, _, lookupErr := db.GetUserByEmail(email)
		if lookupErr != nil {
			return nil, lookupErr
		}
		if existing != nil && existing.Provider == "google" && existing.ProviderSubject == subject {
			return existing, nil
		}
		if existing != nil || errors.Is(err, ErrEmailTaken) {
			return nil, ErrProviderConflict
		}
		return nil, err
	}
	return nil, ErrUsernameTaken
}
