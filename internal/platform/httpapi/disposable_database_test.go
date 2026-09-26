package api

import (
	"database/sql"
	"github.com/google/uuid"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/lib/pq"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func browserSocketTestDatabase(t *testing.T) (*db.Database, *db.Database) {
	t.Helper()
	dsn := os.Getenv("MISTY_BROWSER_SYNC_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable misty_browser_sync_test PostgreSQL")
	}
	base, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var name, user, port, socket string
	if err = base.QueryRow(`SELECT current_database(),current_user,current_setting('port'),current_setting('unix_socket_directories')`).Scan(&name, &user, &port, &socket); err != nil {
		base.Close()
		t.Fatal(err)
	}
	if name != "misty_browser_sync_test" || !strings.HasPrefix(socket, "/tmp/misty-browser-sync-pg.") {
		base.Close()
		t.Fatal("refusing non-disposable database")
	}
	t.Setenv("DB_HOST", socket)
	t.Setenv("DB_PORT", port)
	t.Setenv("DB_USER", user)
	t.Setenv("DB_NAME", name)
	t.Setenv("DB_PASSWORD", "")
	schema := "socket_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("postgres", dsn+" search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := sql.Open("postgres", dsn+" search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	a, b := &db.Database{Conn: conn}, &db.Database{Conn: peer}
	t.Cleanup(func() {
		a.Stop()
		b.Stop()
		base.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
		base.Close()
	})
	if _, err = conn.Exec(`CREATE TABLE users(id text primary key); INSERT INTO users VALUES('owner'),('other')`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "test", "fixtures", "schema-history", "20270212120000_browser_sync.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(strings.Split(string(raw), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	activeMigration, err := os.ReadFile(filepath.Join("..", "..", "..", "test", "fixtures", "schema-history", "20270214120000_browser_sync_active_device.sql"))
	if err != nil {
		t.Fatal(err)
	}
	controlsMigration, err := os.ReadFile(filepath.Join("..", "..", "..", "test", "fixtures", "schema-history", "20270215120000_browser_sync_controls.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(strings.Split(string(activeMigration), "-- +goose Down")[0] + "\n" + strings.Split(string(controlsMigration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	return a, b
}
