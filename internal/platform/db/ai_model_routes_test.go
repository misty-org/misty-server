package db

import (
	"context"
	"database/sql"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"os"
	"strings"
	"testing"
)

func aiProviderTestDB(t *testing.T) *Database {
	t.Helper()
	dsn := os.Getenv("MISTY_AI_PROVIDERS_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable misty_ai_provider_settings_test PostgreSQL")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err = admin.QueryRow(`SELECT current_database()`).Scan(&name); err != nil || name != "misty_ai_provider_settings_test" {
		admin.Close()
		t.Fatal("refusing non-disposable provider settings database")
	}
	schema := "providers_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("postgres", dsn+" search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.Close()
		admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`)
		admin.Close()
	})
	if _, err = conn.Exec(`CREATE TABLE account_cloud_mutations(transaction_id bigint,user_id text,added_bytes bigint,removed_bytes bigint);
 CREATE TABLE users(id text PRIMARY KEY);INSERT INTO users VALUES('owner'),('other');
 CREATE TABLE misty_ask_conversations(id text PRIMARY KEY);
 CREATE FUNCTION misty_rls_user_id() RETURNS text LANGUAGE sql STABLE AS $$ SELECT NULLIF(current_setting('app.current_user_id',true),'') $$;
 CREATE FUNCTION misty_rls_is_service() RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT current_setting('app.rls_mode',true)='service' $$;`); err != nil {
		t.Fatal(err)
	}
	settings, err := migrationFiles.ReadFile("migrations/20271004030000_ai_provider_settings.sql")
	if err != nil {
		t.Fatal(err)
	}
	retire, err := migrationFiles.ReadFile("migrations/20271005080000_retire_ai_provider_keys.sql")
	if err != nil {
		t.Fatal(err)
	}
	settingsUp, _, _ := strings.Cut(string(settings), "-- +goose Down")
	retireUp, retireDown, _ := strings.Cut(string(retire), "-- +goose Down")
	for _, part := range []string{settingsUp, retireUp, retireDown, retireUp} {
		if _, err = conn.Exec(part); err != nil {
			t.Fatal(err)
		}
	}
	return &Database{Conn: conn}
}

func TestAIModelRoutesAreIsolatedAndFrozenPerRun(t *testing.T) {
	database := aiProviderTestDB(t)
	ctx := context.Background()
	routes := []AIModelRoute{{Role: "agent", Model: "anthropic/claude-x", Reasoning: "high"}, {Role: "vision", Model: "google/gemini-x", Reasoning: "high"}}
	frozen, err := database.FreezeAIModelRoutes(ctx, "owner", "run-1", routes)
	if err != nil || len(frozen) != 2 {
		t.Fatal(frozen, err)
	}
	for _, r := range frozen {
		want := map[string]string{"agent": "anthropic/claude-x", "vision": "google/gemini-x"}[r.Role]
		if r.Model != want || r.Reasoning != "high" || !r.Enabled {
			t.Fatal("frozen route lost the reported model or reasoning", r)
		}
	}
	// A later report never changes a running task's models.
	again, err := database.FreezeAIModelRoutes(ctx, "owner", "run-1", []AIModelRoute{{Role: "agent", Model: "openai/other", Reasoning: "low"}})
	if err != nil || len(again) != 2 || modelOf(again, "agent") != "anthropic/claude-x" {
		t.Fatal("running task changed models", again, err)
	}
	if other, err := database.FreezeAIModelRoutes(ctx, "other", "run-1", nil); err != nil || len(other) != 0 {
		t.Fatal("another account read a run route", other, err)
	}
}

func modelOf(routes []AIModelRoute, role string) string {
	for _, r := range routes {
		if r.Role == role {
			return r.Model
		}
	}
	return ""
}
