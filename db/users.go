package db

import (
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID        string
	Name      string
	Email     string
	CreatedAt time.Time
}

func (db *Database) CreateUser(name, email, password string) (*User, error) {
	id := uuid.New().String()
	normalizedEmail := normalizeEmail(email)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	_, err = db.Conn.Exec(
		`INSERT INTO users (id, name, email, password_hash) VALUES ($1, $2, $3, $4)`,
		id, name, normalizedEmail, hash,
	)
	if err != nil {
		log.Println("Failed to create user:", err)
		return nil, err
	}

	return &User{ID: id, Name: name, Email: normalizedEmail, CreatedAt: now}, nil
}

func (db *Database) GetUserByEmail(email string) (*User, string, error) {
	var u User
	var hash string
	normalizedEmail := normalizeEmail(email)

	err := db.Conn.QueryRow(
		`SELECT id, name, email, password_hash, created_at FROM users WHERE LOWER(email) = $1`,
		normalizedEmail,
	).Scan(&u.ID, &u.Name, &u.Email, &hash, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", nil
		}
		log.Println("Failed to get user:", err)
		return nil, "", err
	}

	return &u, hash, nil
}

func (db *Database) UpdateUserName(id, name string) error {
	_, err := db.Conn.Exec(`UPDATE users SET name = $1 WHERE id = $2`, name, id)
	if err != nil {
		log.Println("Failed to update user name:", err)
	}
	return err
}

func (db *Database) GetUserByID(id string) (*User, error) {
	var u User
	err := db.Conn.QueryRow(
		`SELECT id, name, email, created_at FROM users WHERE id = $1`,
		id,
	).Scan(&u.ID, &u.Name, &u.Email, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		log.Println("Failed to get user by ID:", err)
		return nil, err
	}
	return &u, nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
