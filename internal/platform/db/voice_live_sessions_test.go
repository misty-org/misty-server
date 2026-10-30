package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// voiceTestDB applies the voice migrations in a fresh schema of the disposable
// database named by MISTY_VOICE_TEST_DSN (misty_voice_test). The connection
// role must not bypass row-level security, so the owner policy is exercised.
func voiceTestDB(t *testing.T) *Database {
	t.Helper()
	dsn := os.Getenv("MISTY_VOICE_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable misty_voice_test PostgreSQL")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err = admin.QueryRow(`SELECT current_database()`).Scan(&name); err != nil || name != "misty_voice_test" {
		admin.Close()
		t.Fatal("refusing non-disposable database")
	}
	schema := "voice_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("postgres", dsn+" search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() {
		conn.Close()
		admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`)
		admin.Close()
	})
	if _, err = conn.Exec(`CREATE TABLE users(id text PRIMARY KEY); INSERT INTO users VALUES('owner'),('other');
 CREATE FUNCTION misty_rls_user_id() RETURNS text LANGUAGE sql STABLE AS $$ SELECT NULLIF(current_setting('app.current_user_id',true),'') $$;
 CREATE TABLE ai_retrieval_documents(id text PRIMARY KEY, source_kind text NOT NULL, source_id text NOT NULL, owner_user_id text, privacy_class text NOT NULL,
   lifecycle_state text NOT NULL DEFAULT 'active', source_revision text NOT NULL, title text NOT NULL, href text NOT NULL, metadata jsonb NOT NULL DEFAULT '{}', updated_at timestamptz DEFAULT now(), UNIQUE(source_kind, source_id));
 CREATE TABLE ai_retrieval_chunks(document_id text REFERENCES ai_retrieval_documents(id) ON DELETE CASCADE, ordinal int, content text NOT NULL, content_hash text NOT NULL,
   embedding real[], updated_at timestamptz DEFAULT now(), PRIMARY KEY(document_id, ordinal));
 CREATE FUNCTION misty_rls_is_service() RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT current_setting('app.rls_mode',true)='service' $$;`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"20271020000000_voice_live_sessions.sql", "20271020010000_voice_transcripts.sql", "20271021000000_memory_events.sql"} {
		migration, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(migration), "-- +goose Down")
		if _, err = conn.Exec(up); err != nil {
			t.Fatal(name, err)
		}
	}
	// 20271106000000_open_source_no_billing.sql drops the ticket's billing columns.
	if _, err = conn.Exec(`ALTER TABLE voice_live_sessions DROP COLUMN used_ms, DROP COLUMN reservation, DROP COLUMN status, DROP COLUMN settled_at`); err != nil {
		t.Fatal(err)
	}
	// The memory triggers pin search_path to public; tests use their own schema.
	for _, function := range []string{"misty_memory_index_event", "misty_memory_embedded"} {
		if _, err = conn.Exec(`ALTER FUNCTION ` + function + `() SET search_path = ` + pq.QuoteIdentifier(schema) + `, pg_temp`); err != nil {
			t.Fatal(err)
		}
	}
	return &Database{Conn: conn}
}

func TestVoiceLiveSessionsStayWithTheirOwner(t *testing.T) {
	database := voiceTestDB(t)
	ctx := context.Background()
	open := func(user string, expires time.Time) string {
		id := uuid.NewString()
		if err := database.CreateVoiceLiveSession(ctx, VoiceLiveSession{ID: id, User: user, Device: "device", Channels: []string{"mic"}, MaxMS: 600_000, ExpiresAt: expires}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	live := open("owner", time.Now().Add(time.Hour))
	if session, err := database.OpenVoiceLiveSession(ctx, "owner", live); err != nil || session.MaxMS != 600_000 {
		t.Fatalf("open = %+v, %v", session, err)
	}
	// Another account never sees it, and an expired session is gone.
	if _, err := database.OpenVoiceLiveSession(ctx, "other", live); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("other account read the owner's session: %v", err)
	}
	if _, err := database.OpenVoiceLiveSession(ctx, "owner", open("owner", time.Now().Add(-time.Minute))); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("expired session opened: %v", err)
	}
	if err := database.CreateVoiceLiveSession(ctx, VoiceLiveSession{ID: uuid.NewString(), User: "owner", Device: "d", Channels: []string{"speaker"}, MaxMS: 1, ExpiresAt: time.Now()}); err == nil {
		t.Fatal("unknown channel accepted")
	}
}
