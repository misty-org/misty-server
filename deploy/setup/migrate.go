package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/lib/pq"
)

// The database names are fixed: the stack owns its PostgreSQL server.
const (
	dbHost          = "postgres"
	dbName          = "misty"
	dbSuperuser     = "misty"
	dbAppRole       = "misty_app"
	migrationsDir   = "/opt/misty/migrations"
	databaseTimeout = 2 * time.Minute
)

func superuserDSN(store *Store) string { return superuserDSNWith(store.Get("DB_MIGRATION_PASSWORD")) }

func superuserDSNWith(password string) string {
	return fmt.Sprintf("host=%s port=5432 user=%s password=%s dbname=%s sslmode=disable",
		envOr("MISTY_DB_HOST", dbHost), dbSuperuser, password, dbName)
}

// openSuperuser connects as the superuser. PostgreSQL keeps the password it
// was created with, so after a rotation (or adopt-root) the current one fails
// once: migrate then signs in with the previous one and moves the role onto
// the current one.
func openSuperuser(ctx context.Context, store *Store) (*sql.DB, error) {
	database, err := sql.Open("postgres", superuserDSN(store))
	if err != nil {
		return nil, err
	}
	err = waitForDatabase(ctx, database)
	previous := store.Get("DB_MIGRATION_PASSWORD_PREVIOUS")
	if err == nil || !wrongPassword(err) || previous == "" {
		if err != nil {
			database.Close()
			return nil, err
		}
		return database, nil
	}
	database.Close()
	old, err := sql.Open("postgres", superuserDSNWith(previous))
	if err != nil {
		return nil, err
	}
	defer old.Close()
	if err := waitForDatabase(ctx, old); err != nil {
		return nil, err
	}
	statement := `ALTER ROLE ` + pq.QuoteIdentifier(dbSuperuser) + ` WITH PASSWORD ` + pq.QuoteLiteral(store.Get("DB_MIGRATION_PASSWORD"))
	if _, err := old.ExecContext(ctx, statement); err != nil {
		return nil, fmt.Errorf("move the superuser onto its new password: %w", err)
	}
	log.Printf("the database superuser now uses its new password; retire DB_MIGRATION_PASSWORD to drop the old one")
	database, err = sql.Open("postgres", superuserDSN(store))
	if err != nil {
		return nil, err
	}
	if err := waitForDatabase(ctx, database); err != nil {
		database.Close()
		return nil, err
	}
	return database, nil
}

// wrongPassword reports PostgreSQL's invalid_password error.
func wrongPassword(err error) bool {
	var failure *pq.Error
	return errors.As(err, &failure) && failure.Code == "28P01"
}

// runMigrate keeps the API's restricted role in step with the generated
// password, applies every migration and grants the role what it needs. It
// runs on every start and changes no rows itself.
func runMigrate(ctx context.Context) error {
	store, err := openStore(secretsDir())
	if err != nil {
		return err
	}
	if !store.Has("DB_MIGRATION_PASSWORD") || !store.Has("DB_PASSWORD") {
		return fmt.Errorf("the database passwords are missing; the setup service must run first")
	}
	database, err := openSuperuser(ctx, store)
	if err != nil {
		return err
	}
	defer database.Close()
	role := pq.QuoteIdentifier(dbAppRole)
	password := pq.QuoteLiteral(store.Get("DB_PASSWORD"))
	statements := []string{
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '` + dbAppRole + `') THEN CREATE ROLE ` + role + ` LOGIN; END IF; END $$`,
		`ALTER ROLE ` + role + ` WITH LOGIN PASSWORD ` + password + ` NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS`,
		`GRANT CONNECT ON DATABASE ` + pq.QuoteIdentifier(dbName) + ` TO ` + role,
	}
	if err := execAll(ctx, database, statements); err != nil {
		return err
	}
	goose := exec.CommandContext(ctx, envOr("MISTY_GOOSE", "goose"), "-dir", envOr("MISTY_MIGRATIONS_DIR", migrationsDir), "up")
	goose.Env = append(os.Environ(), "GOOSE_DRIVER=postgres", "GOOSE_DBSTRING="+superuserDSN(store))
	goose.Stdout, goose.Stderr = os.Stdout, os.Stderr
	if err := goose.Run(); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	grants := []string{
		`GRANT USAGE ON SCHEMA public TO ` + role,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO ` + role,
		`GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO ` + role,
	}
	if err := execAll(ctx, database, grants); err != nil {
		return err
	}
	log.Printf("database ready")
	return nil
}

func execAll(ctx context.Context, database *sql.DB, statements []string) error {
	for _, statement := range statements {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("prepare database roles: %w", err)
		}
	}
	return nil
}

func waitForDatabase(ctx context.Context, database *sql.DB) error {
	deadline := time.Now().Add(databaseTimeout)
	for {
		err := database.PingContext(ctx)
		if err == nil {
			return nil
		}
		// A wrong password does not get better by waiting.
		if wrongPassword(err) || time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("the database did not answer: %w", err)
		}
		time.Sleep(2 * time.Second)
	}
}
