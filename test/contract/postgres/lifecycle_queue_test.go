package db

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestBoundedSpaceDataPurgeAndSingleRetentionOwner(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("retention-tickets@example.invalid", "password123")
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
