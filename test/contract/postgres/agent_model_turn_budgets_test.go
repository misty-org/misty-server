package db

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAgentModelTurnBudgetRevalidatesAppAuthority(t *testing.T) {
	database, user := invocationOwnerFixture(t)
	ctx := t.Context()
	record, _, err := database.CreateAIInvocationRecord(ctx, AIInvocationRecord{ID: "invocation_" + uuid.NewString(), UserID: user, SurfaceID: "settings", Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	runtime := uuid.NewString()
	if _, err := database.ActivateAIInvocationRuntime(ctx, record.ID, "vercel-workflow", runtime); err != nil {
		t.Fatal(err)
	}
	if err := database.ReserveAgentModelTurn(ctx, user, record.ID, runtime, "model:1"); err != nil {
		t.Fatal(err)
	}
	if err := database.ReserveAgentModelTurn(ctx, user, record.ID, runtime, "model:"+strings.Repeat("x", 201)); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("unbounded model identity: %v", err)
	}
}
