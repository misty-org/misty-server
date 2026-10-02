package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestInvocationStreamPagesHintsIsolationAndRecovery(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Stream", "stream-pages@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Other", "stream-other@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	record, _, err := database.CreateAIInvocationRecord(ctx, AIInvocationRecord{ID: "invocation_stream", UserID: user.ID, SurfaceID: "notes", Mode: "quick", Trigger: "selection", State: "queued", IdempotencyKey: "stream", RequestPayload: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	hint, stop, err := database.SubscribeAIInvocationEvents(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	unrelated, stopOther, err := database.SubscribeAIInvocationEvents(ctx, "unrelated")
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
	peerHint, stopPeer, err := peer.SubscribeAIInvocationEvents(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer stopPeer()
	rollback := errors.New("rollback fixture")
	err = database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state='running' WHERE id=$1`, record.ID); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	resourceQuiet(t, hint)
	resourceQuiet(t, peerHint)
	if _, err := database.CommitAIInvocationEvent(ctx, user.ID, record.ID, "one", "response.delta", json.RawMessage(`{"type":"response.delta","delta":"first"}`), "running"); err != nil {
		t.Fatal(err)
	}
	resourceHint(t, hint)
	resourceHint(t, peerHint)
	resourceQuiet(t, unrelated)
	// Receipt retries and heartbeat-only updates do not emit stream invalidations.
	if _, err := database.CommitAIInvocationEvent(ctx, user.ID, record.ID, "one", "response.delta", json.RawMessage(`{"type":"response.delta","delta":"first"}`), "running"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn.ExecContext(ctx, `UPDATE ai_invocations SET runtime_heartbeat_at=now(),updated_at=now() WHERE id=$1`, record.ID); err != nil {
		t.Fatal(err)
	}
	resourceQuiet(t, hint)
	resourceQuiet(t, peerHint)
	if _, err := peer.ReadAIInvocationEventPage(ctx, other.ID, record.ID, 0); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatal("other account read events", err)
	}
	// Bulk history fixture exercises serialized-byte and row limits independently
	// of the already-tested idempotent event writer.
	err = database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_invocation_events(invocation_id,sequence,event_type,payload,receipt_key)
   SELECT $1,n,'response.delta',jsonb_build_object('type','response.delta','delta',repeat('x',10000)),'bulk-'||n FROM generate_series(2,300)n`, record.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	resourceHint(t, hint)
	resourceHint(t, peerHint)
	if _, err := database.CommitAIInvocationEvent(ctx, user.ID, record.ID, "done", "invocation.completed", json.RawMessage(`{"type":"invocation.completed","state":"completed"}`), "completed"); err != nil {
		t.Fatal(err)
	}
	resourceHint(t, hint)
	resourceHint(t, peerHint)
	cursor := int64(0)
	pages := 0
	for cursor < 301 {
		page, err := peer.ReadAIInvocationEventPage(ctx, user.ID, record.ID, cursor)
		if err != nil || page.State != "completed" || page.Head != 301 || len(page.Events) == 0 || len(page.Events) > AIInvocationPageEvents {
			t.Fatal(page.State, page.Head, len(page.Events), err)
		}
		bytes := 0
		for _, event := range page.Events {
			if event.Sequence != cursor+1 {
				t.Fatal("sequence gap", cursor, event.Sequence)
			}
			cursor = event.Sequence
			bytes += len(event.Payload)
			var wire struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(event.Payload, &wire) != nil || wire.ID == "" {
				t.Fatal("missing normalized ID")
			}
		}
		if bytes > AIInvocationPageBytes {
			t.Fatal("page byte cap", bytes)
		}
		pages++
	}
	if pages < 10 {
		t.Fatal("history was not paginated", pages)
	}
	empty, err := peer.ReadAIInvocationEventPage(ctx, user.ID, record.ID, cursor)
	if err != nil || len(empty.Events) != 0 || empty.Head != cursor {
		t.Fatal(empty, err)
	}
	// One oversized existing event is returned alone, while small events obey
	// the independent row cap. These are explicit history fixtures, not writers.
	if _, err := database.Conn.ExecContext(ctx, `UPDATE ai_invocation_events SET payload=jsonb_build_object('type','response.delta','delta',CASE WHEN sequence=1 THEN repeat('x',524288) ELSE 'small' END) WHERE invocation_id=$1`, record.ID); err != nil {
		t.Fatal(err)
	}
	large, err := peer.ReadAIInvocationEventPage(ctx, user.ID, record.ID, 0)
	if err != nil || len(large.Events) != 1 || len(large.Events[0].Payload) <= AIInvocationPageBytes {
		t.Fatal("oversized progress", len(large.Events), err)
	}
	small, err := peer.ReadAIInvocationEventPage(ctx, user.ID, record.ID, 1)
	if err != nil || len(small.Events) != AIInvocationPageEvents {
		t.Fatal("row cap", len(small.Events), err)
	}
	// Reconnect must deliver a reset even when the deletion notification was lost.
	if _, err := database.Conn.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=current_database() AND application_name='misty-worker-listener'`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn.ExecContext(ctx, `DELETE FROM ai_invocations WHERE id=$1`, record.ID); err != nil {
		t.Fatal(err)
	}
	resourceHint(t, hint)
	resourceHint(t, peerHint)
	if _, err := peer.ReadAIInvocationEventPage(ctx, user.ID, record.ID, 0); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatal("deleted invocation remained readable", err)
	}
	raw, err := os.ReadFile("../../../internal/platform/postgres/migrations/20271001060000_invocation_stream_notifications.sql")
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
