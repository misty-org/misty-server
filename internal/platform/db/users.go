package db

import (
	"context"
	"database/sql"

	"github.com/misty-org/misty-server/internal/accounts"
	"github.com/misty-org/misty-server/internal/avatars"
)

func (db *Database) accountStore() accounts.Store {
	return accounts.Store{Transaction: db.WithRLSContext, CreateIdentity: func(tx *sql.Tx, id, user string) error {
		return createLicenseTx(tx, id, user)
	}}
}

var ErrInvalidUsername = accounts.ErrInvalidUsername
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
type UserBannerReference = accounts.UserBannerReference

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

func (db *Database) UpgradePasswordHash(id, hash, password string) {
	db.accountStore().UpgradePasswordHash(id, hash, password)
}
func (db *Database) ChangePassword(id, current, next, keepSessionHash string) error {
	return db.accountStore().ChangePassword(id, current, next, keepSessionHash)
}
func (db *Database) BumpUserAvatarVersion(id, objectKey string) (int64, error) {
	return db.accountStore().BumpUserAvatarVersion(id, objectKey)
}

func (db *Database) SetUserAvatarDecoration(id string, decoration avatars.Decoration) (avatars.Decoration, error) {
	return db.accountStore().SetUserAvatarDecoration(id, decoration)
}

func (db *Database) RemoveUserAvatar(id string) error {
	return db.accountStore().RemoveUserAvatar(id)
}
func (db *Database) GetUserAvatarVersion(id string) (int64, error) {
	return db.accountStore().GetUserAvatarVersion(id)
}
func (db *Database) GetUserAvatarReference(id string) (UserAvatarReference, error) {
	return db.accountStore().GetUserAvatarReference(id)
}
func (db *Database) BumpUserBannerVersion(id, objectKey string) (int64, error) {
	return db.accountStore().BumpUserBannerVersion(id, objectKey)
}
func (db *Database) RemoveUserBanner(id string) error {
	return db.accountStore().RemoveUserBanner(id)
}
func (db *Database) GetUserBannerReference(id string) (UserBannerReference, error) {
	return db.accountStore().GetUserBannerReference(id)
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
func normalizeEmail(email string) string { return accounts.NormalizeEmail(email) }
func TestingNormalizeUsername(username string) (string, error) {
	return accounts.NormalizeUsername(username)
}

func (db *Database) AnyAccount(ctx context.Context) (bool, error) {
	var exists bool
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users)`).Scan(&exists)
	})
	return exists, err
}
