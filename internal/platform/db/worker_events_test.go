package db

import (
	"context"
	"strings"
	"testing"
	"time"
)

func workerWake(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case _, open := <-ch:
		if !open {
			t.Fatal("subscription closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing worker wakeup")
	}
}
func workerQuiet(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("unexpected worker wakeup")
	case <-time.After(30 * time.Millisecond):
	}
}
func TestWorkerNotificationsCommitRollbackIsolationAndReconnect(t *testing.T) {
	database := workerTestDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ai, stopAI, err := database.SubscribeWorkerEvents(ctx, "library-ai")
	if err != nil {
		t.Fatal(err)
	}
	defer stopAI()
	peerDB := &Database{}
	defer peerDB.Stop()
	peer, stopPeer, err := peerDB.SubscribeWorkerEvents(ctx, "library-ai")
	if err != nil {
		t.Fatal(err)
	}
	defer stopPeer()
	tx, err := database.Conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO library_processing_jobs(id,job_kind,target_id) VALUES('job','ai','item')`); err != nil {
		t.Fatal(err)
	}
	workerQuiet(t, ai)
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	workerQuiet(t, ai)
	if _, err = database.Conn.Exec(`INSERT INTO library_processing_jobs(id,job_kind,target_id) VALUES('job','ai','item')`); err != nil {
		t.Fatal(err)
	}
	workerWake(t, ai)
	workerWake(t, peer)
	if _, err = database.Conn.Exec(`UPDATE library_processing_jobs SET updated_at=now() WHERE id='job'`); err != nil {
		t.Fatal(err)
	}
	workerQuiet(t, ai)
	for i := 0; i < 100; i++ {
		database.workers.publish("library-ai")
	}
	workerWake(t, ai)
	workerQuiet(t, ai)
	if _, err = database.Conn.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
  WHERE datname=current_database() AND application_name='misty-worker-listener'`); err != nil {
		t.Fatal(err)
	}
	workerWake(t, ai)
	workerWake(t, peer)
}

func TestWorkerDeadlinesOnEmptyQueues(t *testing.T) {
	database := workerTestDatabase(t)
	ctx := context.Background()
	for _, queue := range []string{"note-control", "code-session-control"} {
		if _, pending, err := database.NextWorkerDelay(ctx, queue); err != nil || pending {
			t.Fatal("empty queue", queue, pending, err)
		}
	}
}

func TestWorkerMigrationReversesAndReapplies(t *testing.T) {
	database := workerTestDatabase(t)
	migration, err := migrationFiles.ReadFile("migrations/20271001030000_worker_notifications.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(migration), "-- +goose Down")
	workerExec(t, database, parts[1])
	var count int
	if err := database.Conn.QueryRow(`SELECT count(*) FROM pg_trigger WHERE tgrelid IN
  (SELECT oid FROM pg_class WHERE relnamespace=current_schema()::regnamespace) AND NOT tgisinternal`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("migration left %d triggers", count)
	}
	workerExec(t, database, parts[0])
	if _, _, err := database.NextWorkerDelay(context.Background(), "library-ai"); err != nil {
		t.Fatal(err)
	}
}
