package accounts

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"
)

const SessionTTL = 30 * 24 * time.Hour

func (db Store) CreateSession(tokenHash, userID string) error {
	return db.CreateSessionWithTTL(tokenHash, userID, SessionTTL)
}

// CreateSessionWithTTL backs CreateSession and lets callers that did not verify
// a password — the desktop-to-browser handoff — mint a shorter-lived session.
func (db Store) CreateSessionWithTTL(tokenHash, userID string, ttl time.Duration) error {
	now := time.Now()
	expiresAt := now.Add(ttl)
	err := db.Transaction(context.Background(), SessionCreateScope(tokenHash, userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(
			context.Background(),
			`INSERT INTO sessions (token_hash, user_id, expires_at)
			 SELECT $1,$2,$3 FROM users
			 WHERE id=$2 AND lifecycle_state='active'`,
			tokenHash, userID, expiresAt,
		)
		return err
	})
	if err != nil {
		log.Println("Failed to create session:", err)
		return err
	}
	return nil
}

func (db Store) GetSessionUserID(tokenHash string) (string, error) {
	return db.GetSessionUserIDContext(context.Background(), tokenHash)
}

func (db Store) GetSessionUserIDContext(ctx context.Context, tokenHash string) (string, error) {

	var userID string
	err := db.Transaction(ctx, SessionScope(tokenHash), func(tx *sql.Tx) error {
		return tx.QueryRowContext(
			ctx,
			`SELECT user_id FROM sessions
			 WHERE token_hash=$1 AND expires_at>NOW()`,
			tokenHash,
		).Scan(&userID)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}
		log.Println("Failed to get session:", err)
		return "", err
	}
	return userID, nil
}

func (db Store) DeleteSession(tokenHash string) error {
	err := db.Transaction(context.Background(), SessionScope(tokenHash), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
		return err
	})
	if err != nil {
		log.Println("Failed to delete session:", err)
	}
	return err
}
