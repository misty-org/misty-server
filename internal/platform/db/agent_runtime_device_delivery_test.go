package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// deviceDeliveryTestDatabase is a fresh schema in the disposable misty_worker_test database
// with only the tables a device's claim reads.
func deviceDeliveryTestDatabase(t *testing.T) *Database {
	t.Helper()
	if os.Getenv("MISTY_WORKER_TEST") != "1" || os.Getenv("DB_NAME") != "misty_worker_test" {
		t.Skip("requires disposable misty_worker_test PostgreSQL")
	}
	database := &Database{}
	conn, err := sql.Open("postgres", database.GetDSN())
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	database.Conn = conn
	schema := "device_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = conn.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema) + "; SET search_path TO " + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE"); conn.Close() })
	dispatchExec(t, database, `
 CREATE TABLE ai_invocations(id text PRIMARY KEY,user_id text,conversation_id text,runtime_endpoint text);
 CREATE TABLE space_runs(id text PRIMARY KEY,owner_user_id text,runtime_endpoint text);
 CREATE TABLE agent_runtime_deliveries(id text PRIMARY KEY,user_id text NOT NULL,run_id text NOT NULL,operation text NOT NULL,
  payload jsonb DEFAULT '{}' NOT NULL,state text DEFAULT 'pending' NOT NULL,attempts integer DEFAULT 0 NOT NULL,lease_id text,
  lease_expires_at timestamptz,available_at timestamptz DEFAULT now() NOT NULL,last_error text DEFAULT '' NOT NULL,
  created_at timestamptz DEFAULT now() NOT NULL,updated_at timestamptz DEFAULT now() NOT NULL);
 INSERT INTO ai_invocations VALUES('invocation_kept','owner','kept','device:device_a'),('invocation_inbox','owner','inbox',NULL),
  ('invocation_pinned','owner','inbox','device:device_b'),('invocation_other','someone','inbox',NULL);
 INSERT INTO space_runs VALUES('run_task','owner',NULL);`)
	return database
}

func claimedRuns(t *testing.T, database *Database, deviceID string) map[string]AgentRuntimeDelivery {
	t.Helper()
	items, err := database.ClaimDeviceAgentRuntimeDeliveries(context.Background(), "owner", deviceID, 10)
	if err != nil {
		t.Fatal(err)
	}
	claimed := map[string]AgentRuntimeDelivery{}
	for _, item := range items {
		claimed[item.RunID] = item
	}
	return claimed
}

func TestDeviceRuntimeDeliveriesGoToTheComputerTheRunWasSentFrom(t *testing.T) {
	database := deviceDeliveryTestDatabase(t)
	ctx := context.Background()
	for _, run := range []string{"invocation_kept", "invocation_inbox", "invocation_pinned", "invocation_other", "run_task"} {
		if err := database.QueueAgentRuntimeStart(ctx, run); err != nil {
			t.Fatal(run, err)
		}
		// Starting twice queues once.
		if err := database.QueueAgentRuntimeStart(ctx, run); err != nil {
			t.Fatal(run, err)
		}
	}
	if err := database.QueueAgentRuntimeStart(ctx, "invocation_missing"); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatal("a missing run was queued", err)
	}
	if err := database.QueueAgentRuntimeStart(ctx, "whatever"); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatal("a run of no known kind was queued", err)
	}
	var count int
	if err := database.Conn.QueryRow(`SELECT count(*) FROM agent_runtime_deliveries`).Scan(&count); err != nil || count != 5 {
		t.Fatal("starts queued", count, err)
	}

	// Device B gets the run pinned to it, the inbox run and the unpinned task, never device
	// A's chat or another person's run.
	b := claimedRuns(t, database, "device_b")
	for _, run := range []string{"invocation_pinned", "invocation_inbox", "run_task"} {
		if _, ok := b[run]; !ok {
			t.Fatal("device B did not get", run, b)
		}
	}
	if _, ok := b["invocation_kept"]; ok {
		t.Fatal("device B got the run device A sent")
	}
	if _, ok := b["invocation_other"]; ok {
		t.Fatal("device B got another person's run")
	}
	// Leased deliveries aren't handed out again.
	a := claimedRuns(t, database, "device_a")
	if len(a) != 1 || a["invocation_kept"].ID != "start:invocation_kept" {
		t.Fatal("device A should get only its own chat", a)
	}

	// A device finishes only what it holds under its lease.
	held := b["invocation_pinned"]
	if _, err := database.LeasedAgentRuntimeDelivery(ctx, "owner", held.ID, "not-the-lease"); !errors.Is(err, ErrSpaceConflict) {
		t.Fatal("a wrong lease was accepted", err)
	}
	if _, err := database.LeasedAgentRuntimeDelivery(ctx, "someone", held.ID, held.LeaseID); !errors.Is(err, ErrSpaceConflict) {
		t.Fatal("another person's lease was accepted", err)
	}
	got, err := database.LeasedAgentRuntimeDelivery(ctx, "owner", held.ID, held.LeaseID)
	if err != nil || got.RunID != "invocation_pinned" {
		t.Fatal(got, err)
	}

	// A cancel of a run started on device A goes to device A only.
	dispatchExec(t, database, `UPDATE ai_invocations SET runtime_endpoint='device:device_a' WHERE id='invocation_inbox'`)
	if err := database.QueueAgentRuntimeCancel(ctx, "invocation_inbox", "wrun_1"); err != nil {
		t.Fatal(err)
	}
	if b := claimedRuns(t, database, "device_b"); len(b) != 0 {
		t.Fatal("device B got device A's cancel", b)
	}
	if a := claimedRuns(t, database, "device_a"); a["invocation_inbox"].Operation != "runtime.cancel" {
		t.Fatal("device A did not get its cancel", a)
	}
}
