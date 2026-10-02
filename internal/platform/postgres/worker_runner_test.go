package db

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/workqueue"
)

func TestWorkerRunnerSleepsWhenEmptyAndExecutesPersistedDeadline(t *testing.T) {
	database := workerTestDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var reads atomic.Int32
	empty, finished, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	q := workqueue.Queue{
		Subscribe: func(ctx context.Context) (<-chan struct{}, func(), error) {
			return database.SubscribeWorkerEvents(ctx, "note-control")
		},
		Next: func(ctx context.Context) (time.Duration, bool, error) {
			delay, pending, err := database.NextWorkerDelay(ctx, "note-control")
			if reads.Add(1) == 1 {
				close(empty)
			}
			return delay, pending, err
		},
		Process: func(ctx context.Context) (int, error) {
			result, err := database.Conn.ExecContext(ctx, `UPDATE space_note_control_outbox SET delivered_at=now() WHERE delivered_at IS NULL AND next_attempt_at<=now()`)
			if err != nil {
				return 0, err
			}
			n, err := result.RowsAffected()
			if n > 0 {
				close(finished)
			}
			return int(n), err
		},
		OnError: func(err error) { t.Errorf("worker error: %v", err) },
	}
	go func() { defer close(stopped); q.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(2 * time.Second):
			t.Error("worker failed to stop")
		}
	})
	select {
	case <-empty:
	case <-time.After(2 * time.Second):
		t.Fatal("worker failed to initialize")
	}
	time.Sleep(150 * time.Millisecond)
	if reads.Load() != 1 {
		t.Fatal("empty worker performed repeated reads", reads.Load())
	}
	workerExec(t, database, `INSERT INTO space_note_control_outbox VALUES('command',now()+interval '250 milliseconds',NULL)`)
	select {
	case <-finished:
		t.Fatal("ran before persisted deadline")
	case <-time.After(30 * time.Millisecond):
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("missed deadline without notification")
	}
	cancel()
	<-stopped
}
