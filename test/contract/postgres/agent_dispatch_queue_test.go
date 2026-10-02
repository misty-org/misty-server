package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Runs the dispatcher's due-time planning against the full migrated schema.
func TestAgentDispatchDeadlinesOnFullSchema(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	for _, queue := range []string{"agent-runtime", "agent-tasks"} {
		if _, pending, err := database.NextWorkerDelay(ctx, queue); err != nil || pending {
			t.Fatal(queue, "idle schema has dispatcher work", pending, err)
		}
	}
	hints, stop, err := database.SubscribeWorkerEvents(ctx, "agent-runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	user, err := database.CreateUser("Dispatch", "dispatch-deadlines@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	record, _, err := database.CreateAIInvocationRecord(ctx, AIInvocationRecord{DispatchRuntime: true, ID: "invocation_dispatch", UserID: user.ID,
		SurfaceID: "notes", Mode: "quick", Trigger: "selection", State: "queued", IdempotencyKey: "dispatch",
		RequestPayload: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-hints:
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch-ready invocation published no hint")
	}
	delay, pending, err := database.NextWorkerDelay(ctx, "agent-runtime")
	if err != nil || !pending || delay != 0 {
		t.Fatal("start delivery is not due", delay, pending, err)
	}
	deliveries, err := database.ClaimAgentRuntimeDeliveries(ctx, 5)
	if err != nil || len(deliveries) != 1 || deliveries[0].RunID != record.ID {
		t.Fatal("claim", deliveries, err)
	}
	// While leased, the queue sleeps until the lease could be reclaimed.
	if delay, pending, err = database.NextWorkerDelay(ctx, "agent-runtime"); err != nil || !pending || delay < 80*time.Second {
		t.Fatal("leased delivery deadline", delay, pending, err)
	}
	if err := database.FinishAgentRuntimeDelivery(ctx, deliveries[0], nil, false); err != nil {
		t.Fatal(err)
	}
	if _, pending, err = database.NextWorkerDelay(ctx, "agent-runtime"); err != nil || pending {
		t.Fatal("finished delivery leaves dispatcher work", pending, err)
	}
}
