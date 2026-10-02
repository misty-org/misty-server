package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestWorkflowResourceLeaseHintsOwnershipAndDeadlines(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Lease tests", "lease-tests@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Lease tests")
	agent, err := database.EnsureAskIdentity(ctx, owner.ID, "google/gemini-2.5-flash-lite")
	if err != nil {
		t.Fatal(err)
	}
	runA, err := database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, agent.ID, CreatorAgentRunInput{Instruction: "First action"})
	if err != nil {
		t.Fatal(err)
	}
	runB, err := database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, agent.ID, CreatorAgentRunInput{Instruction: "Second action"})
	if err != nil {
		t.Fatal(err)
	}
	key := "private/path/文件"
	hint, stop, err := database.SubscribeWorkflowResourceLeaseEvents(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	other, stopOther, err := database.SubscribeWorkflowResourceLeaseEvents(ctx, "unrelated")
	if err != nil {
		t.Fatal(err)
	}
	defer stopOther()
	peer := &Database{}
	peer.Conn, err = sql.Open("postgres", peer.GetDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Stop()
	peerHint, stopPeer, err := peer.SubscribeWorkflowResourceLeaseEvents(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer stopPeer()
	both := func() { resourceHint(t, hint); resourceHint(t, peerHint); resourceQuiet(t, other) }
	quiet := func() { resourceQuiet(t, hint); resourceQuiet(t, peerHint); resourceQuiet(t, other) }
	rollback := errors.New("rollback fixture")
	err = database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_workflow_resource_leases(resource_key,run_id,node_id,expires_at) VALUES($1,$2,'node',now()+interval '1 minute')`, key, runA.ID); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	quiet()
	if acquired, _, err := database.TryWorkflowResourceLease(ctx, runA.ID, "node", key, "fp", time.Minute); err != nil || !acquired {
		t.Fatal(acquired, err)
	}
	both()
	if acquired, delay, err := peer.TryWorkflowResourceLease(ctx, runB.ID, "node", key, "fp", time.Minute); err != nil || acquired || delay < 50*time.Second || delay > time.Minute {
		t.Fatal(acquired, delay, err)
	}
	quiet()
	// The wrong run cannot release the holder's row or produce a wakeup.
	if err := peer.ReleaseWorkflowResourceLease(ctx, runB.ID, "node", key); err != nil {
		t.Fatal(err)
	}
	quiet()
	// Nested nodes retain existing run-level reentrancy.
	if acquired, _, err := database.TryWorkflowResourceLease(ctx, runA.ID, "nested", key, "fp", time.Minute); err != nil || !acquired {
		t.Fatal(acquired, err)
	}
	both()
	if err := database.ReleaseWorkflowResourceLease(ctx, runA.ID, "node", key); err != nil {
		t.Fatal(err)
	}
	quiet()
	if err := database.ReleaseWorkflowResourceLease(ctx, runA.ID, "nested", key); err != nil {
		t.Fatal(err)
	}
	both()
	if acquired, _, err := peer.TryWorkflowResourceLease(ctx, runB.ID, "node", key, "fp", time.Second); err != nil || !acquired {
		t.Fatal(acquired, err)
	}
	both()
	acquired, delay, err := database.TryWorkflowResourceLease(ctx, runA.ID, "node", key, "fp", time.Minute)
	if err != nil || acquired || delay <= 0 || delay > time.Second {
		t.Fatal(acquired, delay, err)
	}
	// Expiration itself has no notification. The stored deadline is sufficient.
	timer := time.NewTimer(delay + 5*time.Millisecond)
	<-timer.C
	quiet()
	if acquired, _, err := database.TryWorkflowResourceLease(ctx, runA.ID, "node", key, "fp", time.Minute); err != nil || !acquired {
		t.Fatal(acquired, err)
	}
	both()
	// Reconnect broadcasts a reconciliation hint even if the release was missed.
	if _, err := database.Conn.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=current_database() AND application_name='misty-worker-listener'`); err != nil {
		t.Fatal(err)
	}
	if err := database.ReleaseWorkflowResourceLease(ctx, runA.ID, "node", key); err != nil {
		t.Fatal(err)
	}
	resourceHint(t, hint)
	resourceHint(t, peerHint)
	if acquired, _, err := peer.TryWorkflowResourceLease(ctx, runB.ID, "node", key, "fp", time.Minute); err != nil || !acquired {
		t.Fatal(acquired, err)
	}
	// Verify reversible DDL in a transaction; leave the migrated schema intact.
	raw, err := os.ReadFile("../../../internal/platform/postgres/migrations/20271001050000_workflow_resource_notifications.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	err = database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, parts[1]); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, parts[0]); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
}

func resourceHint(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("missing resource hint")
	}
}
func resourceQuiet(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("unexpected resource hint")
	case <-time.After(30 * time.Millisecond):
	}
}
