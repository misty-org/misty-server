package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

func TestDeviceJobStateNotificationsPostgres(t *testing.T) {
	dsn := os.Getenv("MISTY_TRAFFIC_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable misty_traffic_test PostgreSQL")
	}
	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)
	var name string
	if err := conn.QueryRow("SELECT current_database()").Scan(&name); err != nil || name != "misty_traffic_test" {
		t.Fatal("refusing non-test database", err)
	}
	schema := "job_events_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = conn.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema) + "; SET search_path TO " + pq.QuoteIdentifier(schema) + `;
 CREATE TABLE workflow_device_node_jobs(id text, user_id text, state text, last_heartbeat_at timestamptz);
 INSERT INTO workflow_device_node_jobs VALUES('job','owner','executing',now());`)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
	migration, err := migrationFiles.ReadFile("migrations/20271001020000_device_job_state_notifications.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(strings.Split(string(migration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	listener := pq.NewListener(dsn, time.Millisecond, time.Second, nil)
	defer listener.Close()
	if err = listener.Listen("misty_account_events"); err != nil {
		t.Fatal(err)
	}
	quiet := func() {
		t.Helper()
		select {
		case event := <-listener.Notify:
			t.Fatalf("unexpected notification: %+v", event)
		case <-time.After(30 * time.Millisecond):
		}
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE workflow_device_node_jobs SET state='completed' WHERE id='job'`); err != nil {
		t.Fatal(err)
	}
	quiet()
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	quiet()
	if _, err = conn.Exec(`UPDATE workflow_device_node_jobs SET last_heartbeat_at=now(),state=state WHERE id='job'`); err != nil {
		t.Fatal(err)
	}
	quiet()
	if _, err = conn.Exec(`UPDATE workflow_device_node_jobs SET state='completed' WHERE id='job'`); err != nil {
		t.Fatal(err)
	}
	select {
	case notification := <-listener.Notify:
		var event AccountEvent
		if notification == nil || json.Unmarshal([]byte(notification.Extra), &event) != nil ||
			event.Topic != "job-state" || event.UserID != "owner" || event.ID != "job" {
			t.Fatalf("bad event: %+v", notification)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("completion did not notify")
	}
	if _, err = conn.Exec(strings.Split(string(migration), "-- +goose Down")[1]); err != nil {
		t.Fatal("migration rollback", err)
	}
}
