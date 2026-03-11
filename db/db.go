package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

type Database struct {
	Conn *sql.DB
}

func (db *Database) GetDSN() string {
	host := os.Getenv("DB_HOST")
	port := os.Getenv("DB_PORT")
	user := os.Getenv("DB_USER")
	password := os.Getenv("DB_PASSWORD")
	name := os.Getenv("DB_NAME")

	if port == "" {
		port = "5432"
	}

	sslmode := os.Getenv("DB_SSLMODE")
	if sslmode == "" {
		sslmode = "require"
	}

	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		host, port, user, password, name, sslmode,
	)
}

func (db *Database) Start() error {
	conn, err := sql.Open("postgres", db.GetDSN())
	if err != nil {
		log.Println("Failed to open database:", err)
		return err
	}

	if err := conn.Ping(); err != nil {
		log.Println("Failed to connect to database:", err)
		return err
	}

	db.Conn = conn
	return nil
}

func (db *Database) Stop() {
	if db.Conn != nil {
		db.Conn.Close()
	}
}
