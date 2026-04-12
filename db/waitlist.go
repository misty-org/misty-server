package db

import (
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

	result, err := db.Conn.Exec(
		`INSERT INTO waitlist_signups (id, name, email) VALUES ($1, $2, $3)
		 ON CONFLICT (email) DO NOTHING`,
		id, name, normalizedEmail,
	)
	if err != nil {
		log.Println("Failed to create waitlist signup:", err)
		return false, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Println("Failed to inspect waitlist insert result:", err)
		return false, err
	}

	return rowsAffected == 1, nil
}
