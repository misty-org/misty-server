package browsersync

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

func syncTestDatabase(t *testing.T) (*Store, string) {
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
	migration, err := os.ReadFile("../../test/fixtures/schema-history/20270212120000_browser_sync.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(strings.Split(string(migration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	activeMigration, err := os.ReadFile("../../test/fixtures/schema-history/20270214120000_browser_sync_active_device.sql")
	if err != nil {
		t.Fatal(err)
	}
	controlsMigration, controlsErr := os.ReadFile("../../test/fixtures/schema-history/20270215120000_browser_sync_controls.sql")
	if controlsErr != nil {
		t.Fatal(controlsErr)
	}
	if _, err = conn.Exec(strings.Split(string(activeMigration), "-- +goose Down")[0] + "\n" + strings.Split(string(controlsMigration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	// The workspace migration is schema-qualified for production; tests run it in
	// their disposable schema.
	workspacesMigration, err := os.ReadFile("../platform/db/migrations/20270926000000_browser_sync_trees.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(strings.ReplaceAll(strings.Split(string(workspacesMigration), "-- +goose Down")[0], "public.", "")); err != nil {
		t.Fatal(err)
	}
	applyLaterMigrations(t, conn, "../platform/db/migrations")
	return &Store{Conn: conn}, scoped
}

// applyLaterMigrations adds the collections tier (and the per-device flag the
// device list reads), then the vault/workspace naming, to a disposable test
// schema.
func applyLaterMigrations(t *testing.T, conn *sql.DB, dir string) {
	t.Helper()
	for _, name := range []string{"20270929000000_browser_sync_records.sql", "20270930000000_browser_sync_names.sql", "20271001090000_browser_sync_live_connections.sql"} {
		raw, err := os.ReadFile(dir + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		up := strings.Split(string(raw), "-- +goose Down")[0]
		up = strings.NewReplacer("public.", "", "-- +goose StatementBegin", "", "-- +goose StatementEnd", "").Replace(up)
		if _, err = conn.Exec(up); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// Minimal native content tables let this isolated sync schema exercise the
	// same account inventory and write triggers as the complete application.
	if _, err := conn.Exec(`
    CREATE TABLE space_storage_contributions(user_id text,logical_bytes bigint,state text);
    CREATE TABLE space_upload_reservations(user_id text,reserved_bytes bigint,state text);
    CREATE TABLE space_rendition_reservations(user_id text,reserved_bytes bigint,state text);
    CREATE TABLE space_messages(sender_user_id text,content jsonb);
    CREATE TABLE space_notes(creator_user_id text,title_projection text,markdown_projection text,plain_text_projection text,shared_tags jsonb);
    `); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dir + "/20271003120000_account_cloud_measurements.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(strings.Split(string(raw), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	// Minimal Spaces give every person a personal Space before vaults move to
	// one per person per Space.
	if _, err = conn.Exec(`
    CREATE TABLE spaces(id text PRIMARY KEY,owner_user_id text NOT NULL,is_default boolean NOT NULL DEFAULT false,lifecycle_state text NOT NULL DEFAULT 'active');
    CREATE TABLE space_members(space_id text REFERENCES spaces(id) ON DELETE CASCADE,user_id text,role text NOT NULL DEFAULT 'member',PRIMARY KEY(space_id,user_id));
    CREATE FUNCTION misty_ensure_personal_space(target text) RETURNS text LANGUAGE plpgsql AS $f$
    BEGIN
      INSERT INTO spaces(id,owner_user_id,is_default) VALUES('personal_'||target,target,true) ON CONFLICT DO NOTHING;
      INSERT INTO space_members(space_id,user_id,role) VALUES('personal_'||target,target,'owner') ON CONFLICT DO NOTHING;
      RETURN 'personal_'||target;
    END $f$;
    `); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(dir + "/20271028100000_sync_vault_per_space.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.NewReplacer("public.", "", "-- +goose StatementBegin", "", "-- +goose StatementEnd", "").Replace(strings.Split(string(raw), "-- +goose Down")[0])
	if _, err = conn.Exec(up); err != nil {
		t.Fatalf("sync vault per space: %v", err)
	}
}
