package db

import (
	"database/sql"

	"github.com/kannachi323/misty/server/internal/accounts"
)

// Compatibility facade for existing HTTP/tool callers. Account behavior and SQL
// live in accounts; this adapter only supplies the shared RLS transaction.
func (db *Database) accountStore() accounts.Store {
	return accounts.Store{Transaction: db.TestingWithRLSContext, CreateIdentity: func(tx *sql.Tx, id, user string) error {
		_, err := createLicenseTx(tx, id, user, TierBasic, LicenseStatusActive, nil)
		return err
	}}
}

var ErrInvalidUsername = accounts.ErrInvalidUsername
var ErrUsernameTaken = accounts.ErrUsernameTaken

type User = accounts.User
type UserSettings = accounts.UserSettings
type UserAvatarReference = accounts.UserAvatarReference

func (db *Database) CreateUser(name, email, password string) (*User, error) {
	return db.accountStore().CreateUser(name, email, password)
}
func (db *Database) CreateUserWithUsername(name, username, email, password string) (*User, error) {
	return db.accountStore().CreateUserWithUsername(name, username, email, password)
}
func (db *Database) GetUserByEmail(email string) (*User, string, error) {
	return db.accountStore().GetUserByEmail(email)
}
func (db *Database) GetUserByID(id string) (*User, error) { return db.accountStore().GetUserByID(id) }
func (db *Database) UpdateUserName(id, name string) error {
	return db.accountStore().UpdateUserName(id, name)
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
func defaultUsernameForEmail(email string) string { return accounts.DefaultUsernameForEmail(email) }
