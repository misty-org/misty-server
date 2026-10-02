package db

import (
	"context"
	"database/sql"
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func abuseHint(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("missing abuse hint")
	}
}
func abuseQuiet(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("unexpected abuse hint")
	case <-time.After(30 * time.Millisecond):
	}
}
func TestAbuseBlockEventsCommitRollbackClearAndRetention(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	hints, stop, err := database.SubscribeAbuseBlockEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	cleanup, stopCleanup, err := database.SubscribeWorkerEvents(ctx, "abuse-retention")
	if err != nil {
		t.Fatal(err)
	}
	defer stopCleanup()
	// Rollback must not briefly ban a caller on another server.
	sentinel := context.Canceled
	err = database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO abuse_blocks(block_key,blocked_until,block_seconds) VALUES('rolled-back',now()+interval '1 hour',3600)`); err != nil {
			return err
		}
		return sentinel
	})
	if err != sentinel {
		t.Fatal(err)
	}
	abuseQuiet(t, hints)
	abuseQuiet(t, cleanup)
	block := AbuseBlock{Key: "committed", BlockedUntil: time.Now().Add(time.Hour), BlockSeconds: 3600}
	if err := database.SaveAbuseBlock(ctx, block); err != nil {
		t.Fatal(err)
	}
	abuseHint(t, hints)
	abuseHint(t, cleanup)
	delay, pending, err := database.NextWorkerDelay(ctx, "abuse-retention")
	if err != nil || !pending || delay < 24*time.Hour || delay > 25*time.Hour+time.Second {
		t.Fatal("retention deadline", delay, pending, err)
	}
	if err := database.ClearAbuseBlock(ctx, block.Key); err != nil {
		t.Fatal(err)
	}
	abuseHint(t, hints)
	abuseHint(t, cleanup)
	_, pending, err = database.NextWorkerDelay(ctx, "abuse-retention")
	if err != nil || pending {
		t.Fatal("empty retention queue", pending, err)
	}
	for _, key := range []string{"expired-1", "expired-2", "expired-3"} {
		if err := database.SaveAbuseBlock(ctx, AbuseBlock{Key: key, BlockedUntil: time.Now().Add(-25 * time.Hour), BlockSeconds: 60}); err != nil {
			t.Fatal(err)
		}
	}
	delay, pending, err = database.NextWorkerDelay(ctx, "abuse-retention")
	if err != nil || !pending || delay != 0 {
		t.Fatal("due retention", delay, pending, err)
	}
	if n, err := database.PurgeExpiredAbuseBlocks(ctx, 2); err != nil || n != 2 {
		t.Fatal("bounded purge", n, err)
	}
	if n, err := database.PurgeExpiredAbuseBlocks(ctx, 2); err != nil || n != 1 {
		t.Fatal("remaining purge", n, err)
	}
	if n, err := database.PurgeExpiredAbuseBlocks(ctx, 2); err != nil || n != 0 {
		t.Fatal("empty purge", n, err)
	}
}

func TestAbuseGuardAcrossDatabaseInstancesAndReconnect(t *testing.T) {
	database := openTestDatabase(t)
	peer := &Database{}
	var err error
	peer.Conn, err = sql.Open("postgres", peer.GetDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(peer.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	guards := []*api.AbuseGuard{
		api.NewAbuseGuard(api.DefaultAbusePolicy()).WithStore(ctx, database),
		api.NewAbuseGuard(api.DefaultAbusePolicy()).WithStore(ctx, peer),
	}
	done := make(chan struct{}, 2)
	for _, g := range guards {
		go func() { g.Run(ctx, nil); done <- struct{}{} }()
	}
	t.Cleanup(func() {
		cancel()
		for range guards {
			select {
			case <-done:
			case <-time.After(6 * time.Second):
				t.Error("guard failed to stop")
			}
		}
	})
	await := func(key string, want bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			matched := true
			for _, g := range guards {
				blocked, _ := g.Blocked(key)
				matched = matched && blocked == want
			}
			if matched {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("both instances did not reach blocked=%v for %s", want, key)
	}
	block := AbuseBlock{Key: "cross-instance", BlockedUntil: time.Now().Add(time.Minute), BlockSeconds: 60}
	if err := database.SaveAbuseBlock(ctx, block); err != nil {
		t.Fatal(err)
	}
	await(block.Key, true)
	if err := database.ClearAbuseBlock(ctx, block.Key); err != nil {
		t.Fatal(err)
	}
	await(block.Key, false)
	// Both listener connections are terminated in this disposable database.
	// The durable write during reconnect must be recovered without a poll.
	if _, err := database.Conn.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
 WHERE datname=current_database() AND application_name='misty-worker-listener'`); err != nil {
		t.Fatal(err)
	}
	block.Key = "during-reconnect"
	if err := database.SaveAbuseBlock(ctx, block); err != nil {
		t.Fatal(err)
	}
	await(block.Key, true)
}
