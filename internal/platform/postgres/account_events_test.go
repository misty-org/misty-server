package db

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAccountEventIsolationAndOverflow(t *testing.T) {
	a, b := make(chan AccountEvent, 1), make(chan AccountEvent, 1)
	hub := accountEventHub{subs: map[chan AccountEvent]string{a: "a", b: "b"}}
	hub.publish(AccountEvent{UserID: "a", Topic: "runs", ID: "private"})
	if len(b) != 0 {
		t.Fatal("cross-account event")
	}
	hub.publish(AccountEvent{UserID: "a", Topic: "runs", ID: "new"})
	if event := <-a; event.Topic != "reset" {
		t.Fatalf("overflow must reconcile: %+v", event)
	}
	hub.publish(AccountEvent{Topic: "reset"})
	if len(a) != 1 || len(b) != 1 {
		t.Fatal("database reconnect must reset every subscriber")
	}
}

// Opt-in test only runs against the disposable database created for this suite.
// It exercises the actual migration function and transactional LISTEN/NOTIFY.
func TestAccountEventsPostgresCommitRollbackAndReconnect(t *testing.T) {
	if os.Getenv("MISTY_EVENTS_TEST") != "1" || os.Getenv("DB_NAME") != "misty_events_test" {
		t.Skip("requires isolated misty_events_test PostgreSQL")
	}
	database := &Database{}
	conn, err := sql.Open("postgres", database.GetDSN())
	if err != nil {
		t.Fatal(err)
	}
	database.Conn = conn
	defer database.Stop()
	conn.SetMaxOpenConns(1)
	_, err = conn.Exec(`CREATE SCHEMA event_test; SET search_path TO event_test;
CREATE TABLE misty_ask_identities(id text, owner_user_id text);
CREATE TABLE misty_agent_app_assignments(agent_id text, owner_user_id text);
CREATE TABLE space_runs(id text, requesting_member_id text, state text, progress integer);
CREATE TABLE ai_invocations(id text, user_id text, state text);
CREATE TABLE workflow_device_node_jobs(id text, user_id text, state text, last_heartbeat_at timestamptz);
CREATE TABLE agent_run_tool_approvals(id text, owner_user_id text);
CREATE TABLE space_run_approvals(id text, requested_from_user_id text);
CREATE TABLE ai_intervention_waits(id text, user_id text);`)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Exec("DROP SCHEMA event_test CASCADE")
	migration, err := os.ReadFile("../../../test/fixtures/schema-history/20270211000000_account_event_notifications.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := conn.Begin()
	if _, err = tx.Exec(strings.Split(string(migration), "-- +goose Down")[0]); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, stopA, err := database.SubscribeAccountEvents(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer stopA()
	b, stopB, err := database.SubscribeAccountEvents(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	defer stopB()
	if (<-a).Topic != "reset" || (<-b).Topic != "reset" {
		t.Fatal("missing initial snapshot reset")
	}
	// A second API instance has a different listener, but receives the same
	// committed database change without in-process publisher coupling.
	second := &Database{}
	defer second.Stop()
	peer, stopPeer, err := second.SubscribeAccountEvents(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer stopPeer()
	<-peer
	tx, _ = conn.Begin()
	if _, err = tx.Exec(`INSERT INTO space_runs VALUES('run','owner','running',0)`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a:
		t.Fatal("notification before commit")
	case <-time.After(50 * time.Millisecond):
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-a:
		if event.Topic != "runs" || event.ID != "run" {
			t.Fatalf("unexpected event %+v", event)
		}
	case <-ctx.Done():
		t.Fatal("missing committed notification")
	}
	select {
	case <-b:
		t.Fatal("notification leaked to other account")
	default:
	}
	select {
	case event := <-peer:
		if event.ID != "run" {
			t.Fatal(event)
		}
	case <-ctx.Done():
		t.Fatal("second API instance missed commit")
	}
	tx, _ = conn.Begin()
	_, _ = tx.Exec(`UPDATE space_runs SET state='completed' WHERE id='run'`)
	_ = tx.Rollback()
	select {
	case <-a:
		t.Fatal("rollback emitted a notification")
	case <-time.After(50 * time.Millisecond):
	}
	_, err = conn.Exec(`INSERT INTO workflow_device_node_jobs VALUES('job','owner','queued',NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-a:
		if event.Topic != "jobs" {
			t.Fatal(event)
		}
	case <-ctx.Done():
		t.Fatal("missing job notification")
	}
	_, err = conn.Exec(`UPDATE workflow_device_node_jobs SET last_heartbeat_at=NOW(),state='leased' WHERE id='job'`)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-a:
		t.Fatal("lease maintenance must not wake job discovery")
	case <-time.After(50 * time.Millisecond):
	}
	_, err = conn.Exec(`UPDATE space_runs SET state=state,progress=progress WHERE id='run'`)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-a:
		t.Fatal("unchanged progress must not trigger status requests")
	case <-time.After(50 * time.Millisecond):
	}
	stopA()
	reconnected, stop, err := database.SubscribeAccountEvents(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if (<-reconnected).Topic != "reset" {
		t.Fatal("reconnect must reconcile durable state")
	}
}
