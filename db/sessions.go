package db

import (
	"database/sql"
	"errors"
	"log"
	"time"
)

const SessionTTL = 30 * 24 * time.Hour

func (db *Database) CreateSession(tokenHash, userID string) error {
	expiresAt := time.Now().Add(SessionTTL)
	_, err := db.Conn.Exec(
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, expiresAt,
	)
	if err != nil {
		log.Println("Failed to create session:", err)
	}
	return err
}

func (db *Database) GetSessionUserID(tokenHash string) (string, error) {
	var userID string
	err := db.Conn.QueryRow(
		`SELECT user_id FROM sessions WHERE token_hash = $1 AND expires_at > NOW()`,
		tokenHash,
	).Scan(&userID)
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
	_, err := db.Conn.Exec(`DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	if err != nil {
		log.Println("Failed to delete session:", err)
	}
	return err
}
