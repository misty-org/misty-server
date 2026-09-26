package db

import (
	"database/sql"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"os"
	"strings"
	"testing"
)

func syncTestDatabase(t *testing.T) (*Database, string) {
	t.Helper()
	dsn := os.Getenv("MISTY_BROWSER_SYNC_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable misty_browser_sync_test PostgreSQL")
	}
	base, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err = base.QueryRow("SELECT current_database()").Scan(&name); err != nil {
		base.Close()
		t.Fatal(err)
	}
	if name != "misty_browser_sync_test" {
		base.Close()
		t.Fatal("refusing non-disposable database")
	}
	schema := "sync_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema)); err != nil {
		base.Close()
		t.Fatal(err)
	}
	scoped := dsn + " search_path=" + schema
	conn, err := sql.Open("postgres", scoped)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.Close()
		base.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
		base.Close()
	})
	if _, err = conn.Exec(`CREATE TABLE users(id text primary key); INSERT INTO users VALUES('owner'),('other')`); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../../test/fixtures/schema-history/20270212120000_browser_sync.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(strings.Split(string(migration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	activeMigration, err := os.ReadFile("../../../test/fixtures/schema-history/20270214120000_browser_sync_active_device.sql")
	if err != nil {
		t.Fatal(err)
	}
	controlsMigration, controlsErr := os.ReadFile("../../../test/fixtures/schema-history/20270215120000_browser_sync_controls.sql")
	if controlsErr != nil {
		t.Fatal(controlsErr)
	}
	if _, err = conn.Exec(strings.Split(string(activeMigration), "-- +goose Down")[0] + "\n" + strings.Split(string(controlsMigration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	return &Database{Conn: conn}, scoped
}
