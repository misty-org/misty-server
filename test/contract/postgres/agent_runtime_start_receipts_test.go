package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAgentRuntimeStartReceiptsRecoverWithoutResubmission(t *testing.T) {
	database := openTestDatabase(t)
	user, err := database.CreateUser("Start receipts", uuid.NewString()+"@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	create := func() string {
		t.Helper()
		id := "invocation_" + uuid.NewString()
		_, _, err := database.CreateAIInvocationRecord(t.Context(), AIInvocationRecord{ID: id, UserID: user.ID, Mode: "quick", SurfaceID: "settings", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{"prompt":"hello"}`), ExpiresAt: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.BindAgentRuntime(t.Context(), id, "https://worker.test", "https://api.test"); err != nil {
			t.Fatal(err)
		}
		return id
	}
	id := create()
	var wg sync.WaitGroup
	receipts := make(chan *AgentRuntimeStartReceipt, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := database.AgentRuntimeStartReceipt(t.Context(), id, BetaAgentRuntimeAdapter, "https://api.test", "", "")
			if err != nil {
				t.Error(err)
				return
			}
			receipts <- r
		}()
	}
	wg.Wait()
	close(receipts)
	count, token := 0, ""
	for r := range receipts {
		if r.Claimed {
			count++
			token = r.ClaimToken
		} else if r.ClaimToken != "" {
			t.Error("unclaimed worker received submission token")
		}
	}
	if count != 1 || token == "" {
		t.Fatalf("exclusive claims=%d", count)
	}
	if _, err := database.AgentRuntimeStartReceipt(t.Context(), id, BetaAgentRuntimeAdapter, "https://replacement.test", "", ""); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("callback mismatch accepted: %v", err)
	}
	if _, err := database.AgentRuntimeStartReceipt(t.Context(), id, BetaAgentRuntimeAdapter, "https://api.test", "wrong", "engine-1"); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("wrong claim accepted: %v", err)
	}
	for i := 0; i < 2; i++ {
		receipt, err := database.AgentRuntimeStartReceipt(t.Context(), id, BetaAgentRuntimeAdapter, "https://api.test", token, "engine-1")
		if err != nil || receipt.RuntimeRunID != "engine-1" {
			t.Fatalf("record/replay: %#v %v", receipt, err)
		}
	}
	if _, err := database.AgentRuntimeStartReceipt(t.Context(), id, BetaAgentRuntimeAdapter, "https://api.test", token, "engine-2"); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("replaced engine identity: %v", err)
	}
	receipt, err := database.AgentRuntimeStartReceipt(t.Context(), id, BetaAgentRuntimeAdapter, "https://api.test", "", "")
	if err != nil || receipt.Claimed || receipt.RuntimeRunID != "engine-1" {
		t.Fatalf("lost start response not recovered: %#v %v", receipt, err)
	}
	// A crash between engine submission and receipt recording is reconciled from
	// activation itself, which is durable before any tool is allowed to execute.
	active := create()
	if _, err := database.AgentRuntimeStartReceipt(t.Context(), active, BetaAgentRuntimeAdapter, "https://api.test", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateAIInvocationRuntime(t.Context(), active, "vercel-workflow", "engine-active"); err != nil {
		t.Fatal(err)
	}
	receipt, err = database.AgentRuntimeStartReceipt(t.Context(), active, BetaAgentRuntimeAdapter, "https://api.test", "", "")
	if err != nil || receipt.Claimed || receipt.RuntimeRunID != "engine-active" {
		t.Fatalf("activation receipt not recovered: %#v %v", receipt, err)
	}
	canceled := create()
	if err := database.TestingSpaceTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE ai_invocations SET state='canceled' WHERE id=$1`, canceled)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AgentRuntimeStartReceipt(t.Context(), canceled, BetaAgentRuntimeAdapter, "https://api.test", "", ""); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("canceled run admitted: %v", err)
	}
}
