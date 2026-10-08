package db

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func lifecycleHint(t *testing.T, hints <-chan struct{}) {
	t.Helper()
	select {
	case <-hints:
	case <-time.After(5 * time.Second):
		t.Fatal("missing lifecycle hint")
	}
}

func TestScheduledAndCleanupQueuesFollowAccountAISwitch(t *testing.T) {
	database := openTestDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, queue := range []string{"scheduled", "ai-cleanup", "account-deletion", "rendition-reservations"} {
		if _, pending, err := database.NextWorkerDelay(ctx, queue); err != nil || pending {
			t.Fatal(queue, "idle schema has lifecycle work", pending, err)
		}
	}
	scheduled, stopScheduled, err := database.SubscribeWorkerEvents(ctx, "scheduled")
	if err != nil {
		t.Fatal(err)
	}
	defer stopScheduled()
	cleanup, stopCleanup, err := database.SubscribeWorkerEvents(ctx, "ai-cleanup")
	if err != nil {
		t.Fatal(err)
	}
	defer stopCleanup()
	user, err := database.CreateUser("Lifecycle", "lifecycle-queues@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpsertAIRecap(ctx, user.ID, AIRecap{SurfaceID: "activity", Enabled: true, Cadence: "daily", LocalTime: "08:00", Timezone: "UTC", Prompt: "Summarize"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	lifecycleHint(t, scheduled)
	delay, pending, err := database.NextWorkerDelay(ctx, "scheduled")
	if err != nil || !pending || delay <= 0 || delay > 25*time.Hour {
		t.Fatal("recap deadline", delay, pending, err)
	}
	// A claimed schedule is due again only when its lease lapses.
	if _, err := database.Conn.ExecContext(ctx, `UPDATE ai_recaps SET state='running',next_run_at=now()-interval '1 minute',lease_until=now()+interval '10 minutes' WHERE user_id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	lifecycleHint(t, scheduled)
	if delay, pending, err = database.NextWorkerDelay(ctx, "scheduled"); err != nil || !pending || delay < 9*time.Minute {
		t.Fatal("leased recap is due before its lease lapses", delay, pending, err)
	}
	// The account-level AI switch gates claims, so it gates planning too.
	if _, err := database.Conn.ExecContext(ctx, `INSERT INTO ai_user_settings(user_id,enabled) VALUES($1,false)
  ON CONFLICT(user_id) DO UPDATE SET enabled=false`, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, pending, err = database.NextWorkerDelay(ctx, "scheduled"); err != nil || pending {
		t.Fatal("switched-off account keeps a schedule due", pending, err)
	}
	for len(scheduled) > 0 {
		<-scheduled
	}
	// Switching back on makes the retained schedule eligible again and must wake.
	if _, err := database.Conn.ExecContext(ctx, `UPDATE ai_user_settings SET enabled=true WHERE user_id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	lifecycleHint(t, scheduled)
	if _, pending, err = database.NextWorkerDelay(ctx, "scheduled"); err != nil || !pending {
		t.Fatal("re-enabled account schedule is not planned", pending, err)
	}
	// Disabling AI through settings deletes schedules and queues privacy cleanup.
	if _, err := database.UpdateAISettings(ctx, user.ID, false, 30, false, false); err != nil {
		t.Fatal(err)
	}
	lifecycleHint(t, cleanup)
	if _, pending, err = database.NextWorkerDelay(ctx, "scheduled"); err != nil || pending {
		t.Fatal("disabled account keeps a schedule due", pending, err)
	}
	if delay, pending, err = database.NextWorkerDelay(ctx, "ai-cleanup"); err != nil || !pending || delay != 0 {
		t.Fatal("cleanup job not due", delay, pending, err)
	}
}

func TestBoundedSpaceDataPurgeAndSingleRetentionOwner(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Tickets", "retention-tickets@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn.ExecContext(ctx, `INSERT INTO realtime_tickets(token_hash,user_id,after_cursor,expires_at)
  SELECT md5(g::text),$1,0,now()-interval '1 minute' FROM generate_series(1,25) g`, user.ID); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int64{10, 10, 5, 0} {
		if n, err := database.PurgeExpiredSpaceData(ctx, 10); err != nil || n != want {
			t.Fatal("bounded purge", want, n, err)
		}
	}
	var running atomic.Int32
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan bool)
	go func() {
		owned, err := database.WithSessionOwnership(ctx, "retention-test", func(context.Context) error {
			running.Add(1)
			close(held)
			<-release
			return nil
		})
		done <- owned && err == nil
	}()
	<-held
	owned, err := database.WithSessionOwnership(ctx, "retention-test", func(context.Context) error {
		running.Add(1)
		return nil
	})
	if err != nil || owned {
		t.Fatal("second replica ran an owned pass", owned, err)
	}
	close(release)
	if !<-done || running.Load() != 1 {
		t.Fatal("owner did not complete exactly one pass")
	}
	if owned, err = database.WithSessionOwnership(ctx, "retention-test", func(context.Context) error { return nil }); err != nil || !owned {
		t.Fatal("ownership not released", owned, err)
	}
}
