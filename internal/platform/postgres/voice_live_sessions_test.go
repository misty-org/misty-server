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
	if _, err = conn.Exec(`CREATE TABLE account_cloud_mutations(transaction_id bigint,user_id text,added_bytes bigint,removed_bytes bigint);
 CREATE TABLE users(id text PRIMARY KEY); INSERT INTO users VALUES('owner'),('other');
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
	// The memory triggers pin search_path to public; tests use their own schema.
	for _, function := range []string{"misty_memory_index_event", "misty_memory_embedded"} {
		if _, err = conn.Exec(`ALTER FUNCTION ` + function + `() SET search_path = ` + pq.QuoteIdentifier(schema) + `, pg_temp`); err != nil {
			t.Fatal(err)
		}
	}
	return &Database{Conn: conn}
}

func TestVoiceLiveSessionsMeterOnceAndStayWithTheirOwner(t *testing.T) {
	database := voiceTestDB(t)
	ctx := context.Background()
	open := func(user string, maxMS int64) string {
		id := uuid.NewString()
		if err := database.CreateVoiceLiveSession(ctx, VoiceLiveSession{ID: id, User: user, Device: "device", Channels: []string{"mic"}, MaxMS: maxMS,
			Reservation: []byte(`{"id":"r"}`), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	since := time.Now().Add(-time.Hour)
	first := open("owner", 600_000)
	// An open session counts at its full limit.
	if used, err := database.VoiceLiveUsedSince(ctx, "owner", since); err != nil || used != 600_000 {
		t.Fatalf("open usage = %d, %v", used, err)
	}
	// Settling caps at the ticket's limit and counts what was used.
	if session, err := database.CloseVoiceLiveSession(ctx, "owner", first, 900_000, "settled"); err != nil || session.UsedMS != 600_000 {
		t.Fatalf("close = %+v, %v", session, err)
	}
	second := open("owner", 600_000)
	if _, err := database.CloseVoiceLiveSession(ctx, "owner", second, 45_000, "settled"); err != nil {
		t.Fatal(err)
	}
	if used, _ := database.VoiceLiveUsedSince(ctx, "owner", since); used != 645_000 {
		t.Fatalf("settled usage = %d", used)
	}
	// A repeated settle is refused, so billing is never asked twice.
	if session, err := database.CloseVoiceLiveSession(ctx, "owner", second, 45_000, "settled"); !errors.Is(err, ErrVoiceSessionClosed) || session.UsedMS != 45_000 {
		t.Fatalf("second close = %+v, %v", session, err)
	}
	// Another account sees neither the session nor its usage.
	if _, err := database.CloseVoiceLiveSession(ctx, "other", open("owner", 1000), 1, "settled"); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("other account closed the owner's session: %v", err)
	}
	if used, _ := database.VoiceLiveUsedSince(ctx, "other", since); used != 0 {
		t.Fatalf("other account usage = %d", used)
	}
	if err := database.CreateVoiceLiveSession(ctx, VoiceLiveSession{ID: uuid.NewString(), User: "owner", Device: "d", Channels: []string{"speaker"}, MaxMS: 1, Reservation: []byte(`{}`), ExpiresAt: time.Now()}); err == nil {
		t.Fatal("unknown channel accepted")
	}
}
