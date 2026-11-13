package db

import (
	"github.com/misty-org/misty-server/internal/accounts"
)

const SessionTTL = accounts.SessionTTL

// Sessions are uncached so revocation propagates between API processes.
func TestingClearSessionCache() {}
func (db *Database) CreateSession(hash, user string) error {
	return db.accountStore().CreateSession(hash, user)
}
func (db *Database) GetSessionUserID(hash string) (string, error) {
	return db.accountStore().GetSessionUserID(hash)
}
func (db *Database) DeleteSession(hash string) error { return db.accountStore().DeleteSession(hash) }
