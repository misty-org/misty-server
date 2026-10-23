// Package testkit provides infrastructure shared by contract and integration
// suites. It is not imported by production code.
package testkit

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/kannachi323/misty/server/internal/platform/db"

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

	database := &db.Database{Conn: connection, DSN: config.dsn()}
	lockConnection, err := database.Conn.Conn(t.Context())
	if err != nil {
		t.Fatalf("failed to reserve test database lock connection: %v", err)
	}
	if _, err := lockConnection.ExecContext(t.Context(), `SELECT pg_advisory_lock($1)`, databaseLockID); err != nil {
		_ = lockConnection.Close()
		t.Fatalf("failed to acquire test database lock: %v", err)
	}
	resetDatabase(t, database)

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
// take precedence over the scoped development files.
func LoadEnvironment() {
	loadEnvironmentOnce.Do(func() {
		root := repositoryRoot()
		var files []string
		_ = filepath.WalkDir(filepath.Join(root, ".env", "dev"), func(path string, entry fs.DirEntry, err error) error {
			if err == nil && entry.Type().IsRegular() && filepath.Ext(path) == ".env" {
				files = append(files, path)
			}
			return nil
		})
		sort.Strings(files)
		_ = godotenv.Load(files...)
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
	// TRUNCATE replaces every table's files, which dominated the suite's time
	// with ~200 mostly empty tables. Truncate only tables holding rows (checking
	// an empty table is nearly free) and restart only sequences that advanced,
	// which leaves the same clean state as truncating everything.
	_, err := database.Conn.Exec(`
		DO $$
		DECLARE
			item record;
			has_rows boolean;
			dirty text[] := '{}';
		BEGIN
			FOR item IN
				SELECT schemaname, tablename FROM pg_tables
				WHERE schemaname = 'public' AND tablename <> 'goose_db_version'
			LOOP
				EXECUTE format('SELECT EXISTS (SELECT 1 FROM %I.%I)', item.schemaname, item.tablename) INTO has_rows;
				IF has_rows THEN
					dirty := dirty || format('%I.%I', item.schemaname, item.tablename);
				END IF;
			END LOOP;
			IF cardinality(dirty) > 0 THEN
				EXECUTE 'TRUNCATE TABLE ' || array_to_string(dirty, ', ') || ' CASCADE';
			END IF;
			FOR item IN
				SELECT schemaname, sequencename FROM pg_sequences
				WHERE schemaname = 'public' AND last_value IS NOT NULL
					AND sequencename <> 'goose_db_version_id_seq'
			LOOP
				EXECUTE format('ALTER SEQUENCE %I.%I RESTART', item.schemaname, item.sequencename);
			END LOOP;
		END $$;
	`)
	if err != nil {
		t.Fatalf("failed to reset test database: %v", err)
	}
}
