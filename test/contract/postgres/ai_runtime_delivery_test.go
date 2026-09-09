package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"sync"
	"testing"
	"time"
)

func TestInvocationAdmissionAndDeliveryAreAtomic(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Delivery", "delivery@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	input := AIInvocationRecord{ID: "invocation_delivery", UserID: user.ID, SurfaceID: "notes", Mode: "quick", Trigger: "selection", State: "queued", IdempotencyKey: "delivery", RequestPayload: json.RawMessage(`{"prompt":"hello"}`), ExpiresAt: time.Now().Add(time.Hour), DispatchRuntime: true}
	first, created, err := database.CreateAIInvocationRecord(ctx, input)
	if err != nil || !created {
		t.Fatalf("admission: %v", err)
	}
	input.ID = "invocation_retry"
	replay, created, err := database.CreateAIInvocationRecord(ctx, input)
	if err != nil || created || replay.ID != first.ID {
		t.Fatalf("replay: %v", err)
	}
	deliveries, err := database.ClaimAgentRuntimeDeliveries(ctx, 10)
	if err != nil || len(deliveries) != 1 || deliveries[0].RunID != first.ID {
		t.Fatalf("deliveries: %#v %v", deliveries, err)
	}
	next, err := database.ClaimAgentRuntimeDeliveries(ctx, 10)
	if err != nil || len(next) != 0 {
		t.Fatal("leased delivery claimed twice")
	}
	if _, err := database.Conn.ExecContext(ctx, `UPDATE agent_runtime_deliveries SET lease_expires_at=NOW()-INTERVAL '1 second'`); err != nil {
		t.Fatal(err)
	}
	next, err = database.ClaimAgentRuntimeDeliveries(ctx, 10)
	if err != nil || len(next) != 1 || next[0].LeaseID == deliveries[0].LeaseID {
		t.Fatal("crashed delivery was not recovered")
	}
	if err := database.FinishAgentRuntimeDelivery(ctx, deliveries[0], nil, false); !errors.Is(err, ErrSpaceConflict) {
		t.Fatal("stale worker completed a new lease")
	}
	if err := database.FinishAgentRuntimeDelivery(ctx, next[0], nil, false); err != nil {
		t.Fatal(err)
	}
}

func TestInvocationEventsHaveDurableOrderAndReceipts(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Events", "ordered-events@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	invocation, _, err := database.CreateAIInvocationRecord(ctx, AIInvocationRecord{ID: "invocation_order", UserID: user.ID, SurfaceID: "notes", Mode: "quick", Trigger: "selection", State: "queued", IdempotencyKey: "ordered", RequestPayload: json.RawMessage(`{"prompt":"hello"}`), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := database.CommitAIInvocationEvent(ctx, user.ID, invocation.ID, fmt.Sprint("receipt-", i), "response.delta", json.RawMessage(fmt.Sprintf(`{"type":"response.delta","delta":"%d"}`, i)), "running")
			failures <- err
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	events, _, err := database.AIInvocationEvents(ctx, user.ID, invocation.ID, 0)
	if err != nil || len(events) != 20 {
		t.Fatalf("events: %d %v", len(events), err)
	}
	for i, event := range events {
		if event.Sequence != int64(i+1) {
			t.Fatal("sequence gap")
		}
	}
	replay, err := database.CommitAIInvocationEvent(ctx, user.ID, invocation.ID, "receipt-0", "response.delta", json.RawMessage(`{"type":"response.delta","delta":"0"}`), "running")
	if err != nil || replay.Sequence == 0 {
		t.Fatal(err)
	}
	if _, err := database.CommitAIInvocationEvent(ctx, user.ID, invocation.ID, "receipt-0", "response.delta", json.RawMessage(`{"type":"response.delta","delta":"changed"}`), "running"); !errors.Is(err, ErrSpaceConflict) {
		t.Fatal("conflicting receipt accepted")
	}
	if _, err := database.CommitAIInvocationEvent(ctx, user.ID, invocation.ID, "cancel", "invocation.canceled", json.RawMessage(`{"type":"invocation.canceled","state":"canceled"}`), "canceled"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CommitAIInvocationEvent(ctx, user.ID, invocation.ID, "late", "response.delta", json.RawMessage(`{"type":"response.delta","delta":"late"}`), "running"); !errors.Is(err, ErrSpaceConflict) {
		t.Fatal("terminal invocation revived")
	}
}
