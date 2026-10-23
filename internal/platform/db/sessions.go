package db

import (
	"context"
	"time"

	"github.com/kannachi323/misty/server/internal/accounts"
)

const SessionTTL = accounts.SessionTTL

// Sessions are uncached so revocation propagates between API processes.
func TestingClearSessionCache() {}
func (db *Database) CreateSession(hash, user string) error {
	return db.accountStore().CreateSession(hash, user)
}
func (db *Database) CreateSessionWithTTL(hash, user string, ttl time.Duration) error {
	return db.accountStore().CreateSessionWithTTL(hash, user, ttl)
}
func (db *Database) GetSessionUserID(hash string) (string, error) {
	return db.accountStore().GetSessionUserID(hash)
}
func (db *Database) GetSessionUserIDContext(ctx context.Context, hash string) (string, error) {
	return db.accountStore().GetSessionUserIDContext(ctx, hash)
}
func (db *Database) DeleteSession(hash string) error { return db.accountStore().DeleteSession(hash) }
