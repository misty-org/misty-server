package db

import (
	"database/sql"
	"errors"
	"log"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID    string
	Name  string
	Email string
}

func (db *Database) CreateUser(name, email, password string) (*User, error) {
	id := uuid.New().String()
	normalizedEmail := normalizeEmail(email)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	_, err = db.Conn.Exec(
		`INSERT INTO users (id, name, email, password_hash) VALUES ($1, $2, $3, $4)`,
		id, name, normalizedEmail, hash,
	)
	if err != nil {
		log.Println("Failed to create user:", err)
		return nil, err
	}

	return &User{ID: id, Name: name, Email: normalizedEmail}, nil
}

func (db *Database) GetUserByEmail(email string) (*User, string, error) {
	var u User
	var hash string
	normalizedEmail := normalizeEmail(email)

	err := db.Conn.QueryRow(
		`SELECT id, name, email, password_hash FROM users WHERE LOWER(email) = $1`,
		normalizedEmail,
	).Scan(&u.ID, &u.Name, &u.Email, &hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", nil
		}
		log.Println("Failed to get user:", err)
		return nil, "", err
	}

	return &u, hash, nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
