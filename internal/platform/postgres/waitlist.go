package db

import (
	"context"
	"database/sql"
	"log"

	"github.com/google/uuid"
)

type WaitlistSignup struct {
	ID    string
	Name  string
	Email string
}

func (db *Database) CreateWaitlistSignup(name, email string) (bool, error) {
	id := uuid.New().String()
	normalizedEmail := normalizeEmail(email)

	var rowsAffected int64
	err := db.TestingWithRLSContext(context.Background(), waitlistRLSSettings(normalizedEmail), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(
			context.Background(),
			`INSERT INTO waitlist_signups (id, name, email) VALUES ($1, $2, $3)
			 ON CONFLICT (email) DO NOTHING`,
			id, name, normalizedEmail,
		)
		if err != nil {
			return err
		}
		rowsAffected, err = result.RowsAffected()
		return err
	})
	if err != nil {
		log.Println("Failed to create waitlist signup:", err)
		return false, err
	}

	return rowsAffected == 1, nil
}
