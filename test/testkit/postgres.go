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

	"github.com/misty-org/misty-server/internal/platform/db"

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
			"database ping failed for %s@%s:%s/%s: %v\nrun misty server test to bootstrap the test database",
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

func repositoryRoot() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

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

func DatabaseDSN(t testing.TB, user, password string) string {
	t.Helper()
	LoadEnvironment()
	config := loadDatabaseConfig(t)
	config.user = strings.TrimSpace(user)
	config.password = strings.TrimSpace(password)
	return config.dsn()
}

func resetDatabase(t testing.TB, database *db.Database) {
	t.Helper()
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
