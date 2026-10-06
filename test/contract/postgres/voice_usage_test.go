package db

import (
	"context"
	"database/sql"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	"os"
	"strings"
	"testing"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)


import ()

func TestVoiceUsageJournalRecoversKnownUsageButHoldsUnknownWork(t *testing.T) {
	dsn := os.Getenv("MISTY_BILLING_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated billing test database")
	}
	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var name string
	if err = conn.QueryRow("SELECT current_database()").Scan(&name); err != nil || !strings.HasSuffix(name, "_test") {
		t.Fatal("refusing non-test database")
	}
	schema := "voice_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = conn.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec("DROP SCHEMA " + schema + " CASCADE")
	scoped, err := sql.Open("postgres", dsn+" search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	defer scoped.Close()
	for _, path := range []string{"upgrades/20270216000000_billing_adapter_outbox.sql", "upgrades/20270217000000_billing_runtime_groups.sql", "upgrades/20270218000000_billing_admission_intents.sql", "migrations/20270927130000_voice_usage_journal.sql"} {
		raw, err := os.ReadFile("../../../internal/platform/postgres/" + path)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(raw), "-- +goose Down")
		if _, err = scoped.Exec(up); err != nil {
			t.Fatal(err)
		}
	}
	database := &Database{Conn: scoped}
	store := BillingOutbox{Database: database}
	ctx := context.Background()
	r := billingadapter.Reservation{ID: "opaque", Admission: billingadapter.Request{Version: 1, AccountID: "owner", Operation: "agent.voice.realtime", OperationID: "voice", Key: "voice", Usage: billingadapter.Usage{Provider: "openai", Model: "openai/gpt-realtime-2.1"}}}
	if err = store.BeginAdmission(ctx, r.Admission); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveReservation(ctx, r); err != nil {
		t.Fatal(err)
	}
	usage := map[string]int64{"transcription_input_tokens": 15}
	if err = database.RecordVoiceUsage(ctx, &r, "active", usage); err != nil {
		t.Fatal(err)
	}
	foreign := r
	foreign.Admission.AccountID = "foreign"
	if err = database.RecordVoiceUsage(ctx, &foreign, "closed", usage); err == nil {
		t.Fatal("foreign journal rewrite accepted")
	}
	if _, err = scoped.Exec("UPDATE voice_usage_journal SET updated_at=now()-interval '3 minutes'"); err != nil {
		t.Fatal(err)
	}
	if err = database.TestingRecoverVoiceUsage(ctx); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = scoped.QueryRow("SELECT state FROM voice_usage_journal").Scan(&state); err != nil || state != "reconcile" {
		t.Fatal(state, err)
	}
	if err = database.RecordVoiceUsage(ctx, &r, "settlement_pending", usage); err != nil {
		t.Fatal(err)
	}
	if _, err = scoped.Exec("UPDATE voice_usage_journal SET updated_at=now()-interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
	if err = database.TestingRecoverVoiceUsage(ctx); err != nil {
		t.Fatal(err)
	}
	if err = scoped.QueryRow("SELECT state FROM voice_usage_journal").Scan(&state); err != nil || state != "closed" {
		t.Fatal(state, err)
	}
}
