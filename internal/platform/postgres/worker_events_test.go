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
	faces, stopFaces, err := database.SubscribeWorkerEvents(ctx, "library-faces")
	if err != nil {
		t.Fatal(err)
	}
	defer stopFaces()
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
	workerQuiet(t, faces)
	if _, err = database.Conn.Exec(`UPDATE library_processing_jobs SET updated_at=now() WHERE id='job'`); err != nil {
		t.Fatal(err)
	}
	workerQuiet(t, ai)
	// The queue can change hundreds of times while processing: one hint is enough.
	for i := 0; i < 100; i++ {
		database.workers.publish("library-ai")
	}
	workerWake(t, ai)
	workerQuiet(t, ai)
	// Kill only listeners in this disposable database. pq reconnect emits reset
	// and every queue must reconcile, even when notifications were missed.
	if _, err = database.Conn.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
  WHERE datname=current_database() AND application_name='misty-worker-listener'`); err != nil {
		t.Fatal(err)
	}
	workerWake(t, ai)
	workerWake(t, faces)
	workerWake(t, peer)
}

func TestWorkerDeadlinesAndClaimEligibility(t *testing.T) {
	database := workerTestDatabase(t)
	ctx := context.Background()
	for _, queue := range []string{"library-ai", "library-edit", "library-faces", "note-control", "drawing-control", "drawing-purge", "embedding"} {
		if _, pending, err := database.NextWorkerDelay(ctx, queue); err != nil || pending {
			t.Fatal("empty queue", queue, pending, err)
		}
	}
	_, err := database.Conn.Exec(`INSERT INTO library_processing_jobs(id,job_kind,target_id,available_at) VALUES
 ('ai','ai','item',now()+interval '1 hour'),('faces','faces','item',now()+interval '1 hour'),('edit','edit','edit',now()+interval '1 hour')`)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"ai", "edit", "faces"} {
		delay, pending, err := database.NextWorkerDelay(ctx, "library-"+kind)
		if err != nil || !pending || delay < 59*time.Minute || delay > time.Hour {
			t.Fatal(kind, delay, pending, err)
		}
	}
	if _, err = database.Conn.Exec(`UPDATE library_blobs SET lifecycle_state='missing'; UPDATE library_processing_jobs SET available_at=now()`); err != nil {
		t.Fatal(err)
	}
	if _, pending, err := database.NextWorkerDelay(ctx, "library-ai"); err != nil || pending {
		t.Fatal("unavailable source must sleep", pending, err)
	}
	if job, err := database.ClaimLibraryIntelligenceJob(ctx, "worker", time.Minute); err != nil || job != nil {
		t.Fatal("claim and plan disagree", job, err)
	}
	if _, err = database.Conn.Exec(`UPDATE library_blobs SET lifecycle_state='ready'`); err != nil {
		t.Fatal(err)
	}
	if job, err := database.ClaimLibraryIntelligenceJob(ctx, "worker", time.Minute); err != nil || job == nil {
		t.Fatal("AI claim", job, err)
	}
	if job, err := database.ClaimLibraryRenditionJob(ctx, "worker", time.Minute); err != nil || job == nil {
		t.Fatal("edit claim", job, err)
	}
	if job, err := database.ClaimLibraryPeopleJob(ctx, "worker", time.Minute); err != nil || job == nil {
		t.Fatal("faces claim", job, err)
	}
	delay, pending, err := database.NextWorkerDelay(ctx, "library-faces")
	if err != nil || !pending || delay < 50*time.Second {
		t.Fatal("lease deadline", delay, pending, err)
	}
	if _, err = database.Conn.Exec(`UPDATE library_processing_jobs SET lease_expires_at=now()-interval '1 second' WHERE id='faces'`); err != nil {
		t.Fatal(err)
	}
	if job, err := database.ClaimLibraryPeopleJob(ctx, "replacement", time.Minute); err != nil || job == nil || job.AttemptCount != 2 {
		t.Fatal("expired People lease not recovered", job, err)
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
