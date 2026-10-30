package db

import (
	"context"
	"database/sql"

	"github.com/kannachi323/misty/server/internal/accounts"
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
func (db *Database) ChangePassword(id, current, next, keepSessionHash string, credential bool) error {
	return db.accountStore().ChangePassword(id, current, next, keepSessionHash, credential)
}
func (db *Database) SetCredentialPassword(id string, credential bool) error {
	return db.accountStore().SetCredentialPassword(id, credential)
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
// "first account only" server stays closed after its first sign-up.
func (db *Database) AnyAccount(ctx context.Context) (bool, error) {
	var exists bool
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users)`).Scan(&exists)
	})
	return exists, err
}
