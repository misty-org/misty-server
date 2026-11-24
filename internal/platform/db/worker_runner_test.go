package db

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/misty-org/misty-server/internal/platform/workqueue"
)

func TestWorkerRunnerSleepsWhenEmptyAndExecutesPersistedDeadline(t *testing.T) {
	database := workerTestDatabase(t)
	workerExec(t, database, `CREATE TRIGGER abuse_retention_worker_notify AFTER INSERT ON abuse_blocks
 FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('abuse-retention')`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var reads atomic.Int32
	empty, finished, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	q := workqueue.Queue{
		Subscribe: func(ctx context.Context) (<-chan struct{}, func(), error) {
			return database.SubscribeWorkerEvents(ctx, "abuse-retention")
		},
		Next: func(ctx context.Context) (time.Duration, bool, error) {
			delay, pending, err := database.NextWorkerDelay(ctx, "abuse-retention")
			if reads.Add(1) == 1 {
				close(empty)
			}
			return delay, pending, err
		},
		Process: func(ctx context.Context) (int, error) {
			result, err := database.Conn.ExecContext(ctx, `DELETE FROM abuse_blocks WHERE blocked_until+interval '1 day'<=now()`)
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
	workerExec(t, database, `INSERT INTO abuse_blocks VALUES('block',now()-interval '1 day'+interval '250 milliseconds')`)
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
