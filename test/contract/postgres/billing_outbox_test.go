package db

import (
	"context"
	"database/sql"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/billingadapter"
)

func TestBillingOutboxPersistsIdenticalRetriesAndRejectsConflictingUsage(t *testing.T) {
	dsn := os.Getenv("MISTY_BILLING_TEST_DSN")
	if dsn == "" {
		t.Skip("MISTY_BILLING_TEST_DSN requires an isolated PostgreSQL database")
	}
	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var databaseName string
	if err = conn.QueryRow("SELECT current_database()").Scan(&databaseName); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(databaseName, "_test") {
		t.Fatal("refusing a non-test billing database")
	}

	schema := "billing_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = conn.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec("DROP SCHEMA " + schema + " CASCADE")
	scoped, err := sql.Open("postgres", dsn+" search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	defer scoped.Close()

	for _, name := range []string{"20270216000000_billing_adapter_outbox.sql", "20270217000000_billing_runtime_groups.sql", "20270218000000_billing_admission_intents.sql"} {
		migration, err := os.ReadFile("../../../internal/platform/postgres/upgrades/" + name)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(migration), "-- +goose Down")
		if _, err = scoped.Exec(up); err != nil {
			t.Fatal(err)
		}
	}
	store := BillingOutbox{Database: &Database{Conn: scoped}}
	ctx := context.Background()
	entry := billingadapter.Entry{ID: "account-key", Action: "settle", Request: billingadapter.Request{Version: 1, AccountID: "account", Operation: "agent", OperationID: "run", Key: "settle-1", ReservationID: "opaque", Usage: billingadapter.Usage{Units: map[string]int64{"input_tokens": 25}}}}
	reservation := billingadapter.Reservation{ID: "opaque", Admission: entry.Request}
	if err = store.BeginAdmission(ctx, reservation.Admission); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveReservation(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	retained, readErr := store.Reservation(ctx, entry.Request)
	if readErr != nil || retained == nil || retained.ID != "opaque" {
		t.Fatal(retained, readErr)
	}
	changed := entry.Request
	changed.OperationID = "another-run"
	if _, readErr = store.Reservation(ctx, changed); readErr == nil {
		t.Fatal("admission identity conflict was accepted")
	}
	for range 2 {
		if err = store.Enqueue(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := store.Pending(ctx, 10)
	if err != nil || len(entries) != 1 || entries[0].Request.Usage.Units["input_tokens"] != 25 {
		t.Fatal(entries, err)
	}
	entry.Request.Usage.Units["input_tokens"] = 50
	if err = store.Enqueue(ctx, entry); err == nil {
		t.Fatal("conflicting usage replaced original")
	}
	if err = store.Retry(ctx, entry.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	entries, err = store.Pending(ctx, 10)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
	if err = store.Delivered(ctx, entry.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = scoped.QueryRow("SELECT count(*) FROM billing_adapter_outbox WHERE delivered_at IS NOT NULL").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	// An uncertain remote hold cannot become executable after recovery claimed it.
	abandoned := entry.Request
	abandoned.Key = "abandoned"
	if err = store.BeginAdmission(ctx, abandoned); err != nil {
		t.Fatal(err)
	}
	if _, err = scoped.Exec("UPDATE billing_adapter_intents SET expires_at=now()-interval '1 second' WHERE key='abandoned'"); err != nil {
		t.Fatal(err)
	}
	pending, err := store.AbandonedAdmissions(ctx, 20)
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	if err = store.SaveReservation(ctx, billingadapter.Reservation{ID: "late", Admission: abandoned}); err == nil {
		t.Fatal("late receipt permitted abandoned provider work")
	}
	if err = store.BeginAdmission(ctx, abandoned); err == nil {
		t.Fatal("reused abandoned admission")
	}
	if err = store.AdmissionRecovered(ctx, abandoned); err != nil {
		t.Fatal(err)
	}

}
