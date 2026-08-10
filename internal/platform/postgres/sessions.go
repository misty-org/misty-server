package db

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"
)

const SessionTTL = 30 * 24 * time.Hour

func (db *Database) CreateSession(tokenHash, userID string) error {
	return db.CreateSessionWithTTL(tokenHash, userID, SessionTTL)
}

// CreateSessionWithTTL backs CreateSession and lets callers that did not verify
// a password — the desktop-to-browser handoff — mint a shorter-lived session.
func (db *Database) CreateSessionWithTTL(tokenHash, userID string, ttl time.Duration) error {
	expiresAt := time.Now().Add(ttl)
	err := db.TestingWithRLSContext(context.Background(), sessionCreateRLSSettings(tokenHash, userID), func(tx *sql.Tx) error {
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
	}
	return err
}

func (db *Database) GetSessionUserID(tokenHash string) (string, error) {
	var userID string
	err := db.TestingWithRLSContext(context.Background(), sessionRLSSettings(tokenHash), func(tx *sql.Tx) error {
		return tx.QueryRowContext(
			context.Background(),
			`SELECT user_id FROM sessions
			 WHERE token_hash=$1 AND expires_at>NOW()`,
			tokenHash,
		).Scan(&userID)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		log.Println("Failed to get session:", err)
		return "", err
	}
	return userID, nil
}

func (db *Database) DeleteSession(tokenHash string) error {
	err := db.TestingWithRLSContext(context.Background(), sessionRLSSettings(tokenHash), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
		return err
	})
	if err != nil {
		log.Println("Failed to delete session:", err)
	}
	return err
}
