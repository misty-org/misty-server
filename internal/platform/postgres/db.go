package db

import (
	"database/sql"
	"embed"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	_ "github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Database struct {
	Conn *sql.DB
}

func (db *Database) GetDSN() string {
	host := envconfig.Getenv("DB_HOST")
	port := envconfig.Getenv("DB_PORT")
	user := envconfig.Getenv("DB_USER")
	password := envconfig.Getenv("DB_PASSWORD")
	name := envconfig.Getenv("DB_NAME")

	if port == "" {
		port = "5432"
	}

	sslmode := TestingDatabaseSSLMode(host)

	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		host, port, user, password, name, sslmode,
	)
}

func TestingDatabaseSSLMode(host string) string {
	host = strings.TrimSpace(host)
	if strings.HasPrefix(host, "/") || strings.EqualFold(host, "localhost") || strings.EqualFold(host, "postgres") {
		return "disable"
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil && ip.IsLoopback() {
		return "disable"
	}
	return "require"
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
	warnIfRoleBypassesRLS(conn)

	db.Conn = conn
	if err := db.TestingCheckSchemaVersion(); err != nil {
		return err
	}
	return nil
}

// checkSchemaVersion fails fast with a clear error when the database hasn't
// had every migration applied, instead of letting the server start against a
// stale schema — which manifests later as scattered, hard-to-diagnose
// failures (400s on requests that touch changed tables, dropped WebSocket
// connections, etc.) rather than one obvious error at boot.
func (db *Database) TestingCheckSchemaVersion() error {
	expected, err := TestingLatestMigrationVersion()
	if err != nil {
		return fmt.Errorf("determine expected schema version: %w", err)
	}
	var applied int64
	if err := db.Conn.QueryRow(
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = true`,
	).Scan(&applied); err != nil {
		return fmt.Errorf(
			"read applied schema version (run ./scripts/goose.sh up if migrations have never been applied): %w", err,
		)
	}
	if applied < expected {
		return fmt.Errorf(
			"database schema is out of date: applied migration %d, latest migration is %d — run ./scripts/goose.sh up",
			applied, expected,
		)
	}
	return nil
}

func TestingLatestMigrationVersion() (int64, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return 0, err
	}
	var latest int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		underscoreIndex := strings.Index(entry.Name(), "_")
		if underscoreIndex <= 0 {
			continue
		}
		version, err := strconv.ParseInt(entry.Name()[:underscoreIndex], 10, 64)
		if err != nil {
			continue
		}
		if version > latest {
			latest = version
		}
	}
	if latest == 0 {
		return 0, fmt.Errorf("no migration files found")
	}
	return latest, nil
}

func (db *Database) Stop() {
	if db.Conn != nil {
		db.Conn.Close()
	}
}

func warnIfRoleBypassesRLS(conn *sql.DB) {
	var role string
	var bypassesRLS bool
	err := conn.QueryRow(`
		SELECT rolname, rolsuper OR rolbypassrls
		FROM pg_roles
		WHERE rolname = current_user
	`).Scan(&role, &bypassesRLS)
	if err != nil {
		log.Println("Failed to inspect database role RLS settings:", err)
		return
	}
	if bypassesRLS {
		log.Printf("WARNING: database role %q bypasses row-level security; use a non-superuser role without BYPASSRLS in production", role)
	}
}
