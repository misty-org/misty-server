package db

import (
	"context"
	"database/sql"

	"github.com/misty-org/misty-server/internal/accounts"
)

// Compatibility facade for existing HTTP/tool callers. Account behavior and SQL
// live in accounts; this adapter only supplies the shared RLS transaction.
func (db *Database) accountStore() accounts.Store {
	return accounts.Store{Transaction: db.WithRLSContext, CreateIdentity: func(tx *sql.Tx, id, user string) error {
		return createLicenseTx(tx, id, user)
	}}
}

var ErrInvalidUsername = accounts.ErrInvalidUsername
var ErrInvalidPassword = accounts.ErrInvalidPassword
var ErrUsernameTaken = accounts.ErrUsernameTaken
var ErrPasswordAccount = accounts.ErrPasswordAccount
var ErrEmailTaken = accounts.ErrEmailTaken
var ErrWrongCurrentPassword = accounts.ErrWrongCurrentPassword
var ErrNoPasswordAccount = accounts.ErrNoPasswordAccount
var ErrProviderConflict = accounts.ErrProviderConflict

func (db *Database) GoogleUser(email, subject string) (*User, error) {
	return db.accountStore().GoogleUser(email, subject)
}

type User = accounts.User
type UserSettings = accounts.UserSettings
type UserAvatarReference = accounts.UserAvatarReference

func (db *Database) CreateUser(email, password string) (*User, error) {
	return db.accountStore().CreateUser(email, password)
}
func (db *Database) CreateUserWithUsername(username, email, password string) (*User, error) {
	return db.accountStore().CreateUserWithUsername(username, email, password)
}
func (db *Database) GetUserByEmail(email string) (*User, string, error) {
	return db.accountStore().GetUserByEmail(email)
}
func (db *Database) GetUserByID(id string) (*User, error) { return db.accountStore().GetUserByID(id) }
func (db *Database) ChangePassword(id, current, next, keepSessionHash string) error {
	return db.accountStore().ChangePassword(id, current, next, keepSessionHash)
}
func (db *Database) BumpUserAvatarVersion(id string) (int64, error) {
	return db.accountStore().BumpUserAvatarVersion(id)
}
func (db *Database) GetUserAvatarVersion(id string) (int64, error) {
	return db.accountStore().GetUserAvatarVersion(id)
}
func (db *Database) GetUserAvatarReference(id string) (UserAvatarReference, error) {
	return db.accountStore().GetUserAvatarReference(id)
}
func (db *Database) SetUsername(id, username string) (string, error) {
	return db.accountStore().SetUsername(id, username)
}
func (db *Database) GetUserSettingsByID(id string) (*UserSettings, error) {
	return db.accountStore().GetUserSettingsByID(id)
}
func (db *Database) UpdateUserSettings(id string, s UserSettings) error {
	return db.accountStore().UpdateUserSettings(id, s)
}
func (db *Database) UpdateTelemetryPreferences(id string, a, e bool) error {
	return db.accountStore().UpdateTelemetryPreferences(id, a, e)
}
func normalizeEmail(email string) string { return accounts.NormalizeEmail(email) }
func TestingNormalizeUsername(username string) (string, error) {
	return accounts.NormalizeUsername(username)
}

// AnyAccount reports whether any account exists, deleted ones included, so a
// server stays closed to self sign-up after its first account.
func (db *Database) AnyAccount(ctx context.Context) (bool, error) {
	var exists bool
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users)`).Scan(&exists)
	})
	return exists, err
}

// PendingInvitationForEmail reports whether a live Space invitation (not
// accepted, revoked or expired) names this email, which lets its owner create
// an account on a server that takes new accounts only by invitation.
func (db *Database) PendingInvitationForEmail(ctx context.Context, email string) (bool, error) {
	email = normalizeEmail(email)
	if email == "" {
		return false, nil
	}
	var exists bool
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM space_invitations
			WHERE lower(invited_email)=$1 AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>NOW()
		)`, email).Scan(&exists)
	})
	return exists, err
}
