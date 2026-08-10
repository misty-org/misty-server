// Package testkit provides infrastructure shared by contract and integration
// suites. It is not imported by production code.
package testkit

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/joho/godotenv"
)

const databaseLockID int64 = 621042

var loadEnvironmentOnce sync.Once

type databaseConfig struct {
	host     string
	port     string
	user     string
	password string
	name     string
	sslmode  string
}

// OpenDatabase opens, serializes, and resets the destructive test database.
func OpenDatabase(t testing.TB) *db.Database {
	t.Helper()
	LoadEnvironment()
	config := loadDatabaseConfig(t)

	connection, err := sql.Open("postgres", config.dsn())
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if err := connection.Ping(); err != nil {
		_ = connection.Close()
		t.Fatalf(
			"database ping failed for %s@%s:%s/%s: %v\nrun ./test.sh to bootstrap the test database",
			config.user, config.host, config.port, config.name, err,
		)
	}

	database := &db.Database{Conn: connection}
	lockConnection, err := database.Conn.Conn(t.Context())
	if err != nil {
		t.Fatalf("failed to reserve test database lock connection: %v", err)
	}
	if _, err := lockConnection.ExecContext(t.Context(), `SELECT pg_advisory_lock($1)`, databaseLockID); err != nil {
		_ = lockConnection.Close()
		t.Fatalf("failed to acquire test database lock: %v", err)
	}
	resetDatabase(t, database)
	operator, _, err := database.GetUserByEmail("test-misty-operator@example.com")
	if err != nil {
		t.Fatalf("find canonical Misty test operator: %v", err)
	}
	if operator == nil {
		operator, err = database.CreateUserWithUsername(
			"Test Misty Operator",
			"test_misty_operator",
			"test-misty-operator@example.com",
			"password123",
		)
		if err != nil {
			t.Fatalf("create canonical Misty test operator: %v", err)
		}
	}
	if err := database.ConfigureCanonicalMistySpace(t.Context(), operator.ID); err != nil {
		t.Fatalf("configure canonical Misty test Space: %v", err)
	}

	t.Cleanup(func() {
		resetDatabase(t, database)
		if _, err := lockConnection.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, databaseLockID); err != nil {
			t.Fatalf("failed to release test database lock: %v", err)
		}
		if err := lockConnection.Close(); err != nil {
			t.Fatalf("failed to close test database lock connection: %v", err)
		}
		database.Stop()
	})
	return database
}

// LoadEnvironment loads the repository dotenv files at most once for suites
// that need a secret before opening their first database connection.
//
// go test runs each package with its own directory as the working directory,
// so the files are resolved from the repository root rather than relative to
// the caller. godotenv never overwrites a value already present in the
// process environment, so test.sh's exports and CI's job environment still
// take precedence over both files.
func LoadEnvironment() {
	loadEnvironmentOnce.Do(func() {
		root := repositoryRoot()
		for _, name := range []string{".env", ".env.dev"} {
			_ = godotenv.Load(filepath.Join(root, name))
		}
	})
}

// repositoryRoot resolves the checkout root from this file's compile-time
// path, which is stable no matter which package's tests are running.
func repositoryRoot() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

// ApplyDatabaseEnvironment points DB_* at the resolved test database for the
// duration of the test. Suites that let production code open its own
// connection from the environment use this instead of resolving TEST_DB_*
// themselves, so every suite agrees on which database is the test database.
func ApplyDatabaseEnvironment(t *testing.T) {
	t.Helper()
	LoadEnvironment()
	config := loadDatabaseConfig(t)
	t.Setenv("DB_HOST", config.host)
	t.Setenv("DB_PORT", config.port)
	t.Setenv("DB_USER", config.user)
	t.Setenv("DB_PASSWORD", config.password)
	t.Setenv("DB_NAME", config.name)
	t.Setenv("DB_SSLMODE", config.sslmode)
}

// DatabaseDSN returns the test database connection string with a caller-owned
// runtime role substituted for the administrative role.
func DatabaseDSN(t testing.TB, user, password string) string {
	t.Helper()
	LoadEnvironment()
	config := loadDatabaseConfig(t)
	config.user = strings.TrimSpace(user)
	config.password = strings.TrimSpace(password)
	return config.dsn()
}

// loadDatabaseConfig resolves the destructive test database connection.
//
// Each field falls back from TEST_DB_* to the matching DB_* value, applying
// the same derivations test.sh applies when it bootstraps the container, so a
// bare `go test ./...` against an already-bootstrapped database resolves the
// same connection the full harness does.
func loadDatabaseConfig(t testing.TB) databaseConfig {
	t.Helper()
	config := databaseConfig{
		host: testDatabaseHost(),
		port: firstConfigured("TEST_DB_PORT", "DB_PORT"),
		name: testDatabaseName(),
	}
	config.user, config.password = testDatabaseRole()
	if config.port == "" {
		config.port = "5432"
	}
	config.sslmode = testDatabaseSSLMode(config.host)

	switch {
	case config.host == "":
		t.Fatal(missingDatabaseEnvironment("host", "TEST_DB_HOST or DB_HOST"))
	case config.user == "":
		t.Fatal(missingDatabaseEnvironment("user", "TEST_DB_USER, DB_MIGRATION_USER, or DB_USER"))
	case config.password == "":
		t.Fatal(missingDatabaseEnvironment("password", "TEST_DB_PASSWORD, DB_MIGRATION_PASSWORD, or DB_PASSWORD"))
	case config.name == "":
		t.Fatal(missingDatabaseEnvironment("name", "TEST_DB_NAME or DB_NAME"))
	}
	if !strings.Contains(strings.ToLower(config.name), "test") {
		t.Fatalf("refusing to reset non-test database %q", config.name)
	}
	return config
}

func testDatabaseHost() string {
	host := firstConfigured("TEST_DB_HOST", "DB_HOST")
	switch host {
	case "localhost", "::1":
		// The development Postgres port is intentionally IPv4 loopback-only,
		// and macOS resolves localhost to ::1 first. test.sh makes the same
		// substitution when it bootstraps the container.
		return "127.0.0.1"
	}
	return host
}

// testDatabaseName never returns the development database: a DB_NAME without
// "test" in it gains the same _test suffix test.sh creates.
func testDatabaseName() string {
	if name := configured("TEST_DB_NAME"); name != "" {
		return name
	}
	name := configured("DB_NAME")
	if name == "" || strings.Contains(strings.ToLower(name), "test") {
		return name
	}
	return name + "_test"
}

// testDatabaseRole prefers the migration role over DB_USER. DB_USER is the
// application role and is deliberately unprivileged, so it can neither create
// the test database nor TRUNCATE between tests.
func testDatabaseRole() (user, password string) {
	if user := configured("TEST_DB_USER"); user != "" {
		return user, configured("TEST_DB_PASSWORD")
	}
	if user := configured("DB_MIGRATION_USER"); user != "" {
		return user, firstConfigured("DB_MIGRATION_PASSWORD", "DB_PASSWORD")
	}
	return configured("DB_USER"), configured("DB_PASSWORD")
}

// testDatabaseSSLMode ignores a production DB_SSLMODE for loopback hosts. The
// bootstrapped container has no TLS, so inheriting sslmode=require from
// .env.dev would fail every connection.
func testDatabaseSSLMode(host string) string {
	if mode := configured("TEST_DB_SSLMODE"); mode != "" {
		return mode
	}
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return "disable"
	}
	if mode := configured("DB_SSLMODE"); mode != "" {
		return mode
	}
	return "disable"
}

func missingDatabaseEnvironment(field, names string) string {
	return fmt.Sprintf(
		"missing integration DB %s; set %s, or run ./test.sh to bootstrap the test database",
		field, names,
	)
}

func configured(name string) string {
	return strings.TrimSpace(envconfig.Getenv(name))
}

func firstConfigured(names ...string) string {
	for _, name := range names {
		if value := configured(name); value != "" {
			return value
		}
	}
	return ""
}

func (config databaseConfig) dsn() string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		config.host, config.port, config.user, config.password, config.name, config.sslmode,
	)
}

// resetDatabase truncates every application table.
//
// The table list is read from the catalog rather than hand-maintained. It used
// to name six tables explicitly, which stopped covering the schema as
// migrations added tables: rows in tables reachable from users only through
// TRUNCATE ... CASCADE were cleared, and everything else survived. Tables with
// no foreign key into users — stripe_webhook_events above all — accumulated
// across runs, so a test replaying a fixed Stripe event ID passed on a freshly
// bootstrapped database and failed on every rerun.
//
// Migrations seed no static reference data (their INSERTs all backfill from
// spaces/users), so nothing here needs preserving except goose_db_version,
// which records the schema version checkSchemaVersion reads.
func resetDatabase(t testing.TB, database *db.Database) {
	t.Helper()
	_, err := database.Conn.Exec(`
		DO $$
		DECLARE statement text;
		BEGIN
			SELECT 'TRUNCATE TABLE ' ||
				string_agg(format('%I.%I', schemaname, tablename), ', ') ||
				' RESTART IDENTITY CASCADE'
			INTO statement
			FROM pg_tables
			WHERE schemaname = 'public' AND tablename <> 'goose_db_version';
			IF statement IS NOT NULL THEN
				EXECUTE statement;
			END IF;
		END $$;
	`)
	if err != nil {
		t.Fatalf("failed to reset test database: %v", err)
	}
}
