package db

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/aimodels"
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
func TestAIModelSensesAreIsolatedAndFrozenPerRun(t *testing.T) {
	database := aiProviderTestDB(t)
	ctx := context.Background()
	thinking, _ := aimodels.FindSense("thinking")
	seeing, _ := aimodels.FindSense("seeing")
	if err := database.SaveAIModelSense(ctx, "owner", thinking, "bad model"); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatal("invalid model saved", err)
	}
	if err := database.SaveAIModelSense(ctx, "owner", thinking, "anthropic/claude-x"); err != nil {
		t.Fatal(err)
	}
	if err := database.SaveAIModelSense(ctx, "owner", seeing, "google/gemini-x"); err != nil {
		t.Fatal(err)
	}
	if routes, err := database.AIModelRoutes(ctx, "other"); err != nil || len(routes) != 0 {
		t.Fatal("model choices leaked across accounts", routes, err)
	}
	owned, err := database.AIModelRoutes(ctx, "owner")
	if err != nil || len(owned) != 3 {
		t.Fatal("seeing did not set both of its roles", owned, err)
	}
	defaults := []aimodels.Route{}
	for _, role := range aimodels.Roles {
		defaults = append(defaults, aimodels.Route{Role: role.ID, Model: "openai/default", Reasoning: "high", Enabled: true})
	}
	frozen, err := database.FreezeAIModelRoutes(ctx, "owner", "run-1", defaults, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range frozen {
		want := map[string]string{"agent": "anthropic/claude-x", "vision": "google/gemini-x", "library": "google/gemini-x"}[r.Role]
		if want == "" {
			want = "openai/default"
		}
		if r.Model != want || r.Reasoning != "high" {
			t.Fatal("frozen route ignored the account's choice or the admitted reasoning", r)
		}
	}
	if err := database.SaveAIModelSense(ctx, "owner", thinking, ""); err != nil {
		t.Fatal(err)
	}
	if agent, _ := database.AIModelRunRoute(ctx, "owner", "run-1", "agent"); agent.Model != "anthropic/claude-x" {
		t.Fatal("running task changed with account preferences", agent)
	}
	if _, err = database.AIModelRunRoute(ctx, "other", "run-1", "agent"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("another account read a run route", err)
	}
	if _, err := database.FreezeAIModelRoutes(ctx, "owner", "run-2", defaults, map[string]string{"agent": "spacexai/grok-x"}); err != nil {
		t.Fatal(err)
	}
	if agent, _ := database.AIModelRunRoute(ctx, "owner", "run-2", "agent"); agent.Model != "spacexai/grok-x" {
		t.Fatal("a conversation's own model did not win", agent)
	}
	if vision, _ := database.AIModelRunRoute(ctx, "owner", "run-2", "vision"); vision.Model != "google/gemini-x" {
		t.Fatal("a conversation override leaked into another role", vision)
	}
}
