package db

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestManualRoutineAdmissionPinsScopeAndDispatchesAtomically(t *testing.T) {
	database, appctx, user, pin := sdkInvocationFixture(t)
	ctx := context.Background()
	id, request := uuid.NewString(), uuid.NewString()
	var draft map[string]any
	if err := json.Unmarshal(routineDraftFixture(t, "Habit routine"), &draft); err != nil {
		t.Fatal(err)
	}
	step := draft["steps"].([]any)[0].(map[string]any)
	step["action"] = cap.RoutinePin{Capability: pin.Capability, CapabilityVersion: pin.CapabilityVersion, ProviderID: pin.ProviderID, ProviderVersion: pin.ProviderVersion, TargetID: pin.TargetID, TargetRevision: pin.TargetRevision}
	raw, _ := json.Marshal(draft)
	if _, err := database.SaveRoutineDraft(ctx, user, id, 0, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AdmitManualRoutine(appctx, user, id, request, 1, json.RawMessage(`{}`)); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("app self admission: %v", err)
	}
	run, err := database.AdmitManualRoutine(ctx, user, id, request, 1, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := database.AdmitManualRoutine(ctx, user, id, request, 1, json.RawMessage(`{}`))
	if err != nil || replay.Execution.RunID != run.Execution.RunID || replay.Execution.Bindings[0].CallID != run.Execution.Bindings[0].CallID {
		t.Fatalf("changed admission identity: %#v %v", replay, err)
	}
	if _, err := database.AdmitManualRoutine(ctx, user, id, request, 1, json.RawMessage(`{"changed":true}`)); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("changed trigger replay: %v", err)
	}
	if _, err := database.AdmitManualRoutine(ctx, user, id, uuid.NewString(), 1, json.RawMessage(`{}`)); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("overlapping routine: %v", err)
	}
	bindings, err := database.AgentSDKCapabilityBindings(ctx, user, run.Execution.RunID)
	if err != nil || len(bindings) != 1 || bindings[0].TargetID != pin.TargetID {
		t.Fatalf("pinned scope: %#v %v", bindings, err)
	}
	deliveries, err := database.ClaimAgentRuntimeDeliveries(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, delivery := range deliveries {
		if delivery.RunID == run.Execution.RunID && delivery.Operation == "invocation.start" {
			found = true
		}
	}
	if !found {
		t.Fatal("run has no durable dispatch intent")
	}
	if err := database.RequestRoutineCancellation(ctx, user, run.Execution.RunID); err != nil {
		t.Fatal(err)
	}
	cancelled, err := database.RoutineRun(ctx, user, run.Execution.RunID)
	if err != nil || cancelled.Outcome != "cancelled" || cancelled.State != "canceled" {
		t.Fatalf("pre-activation cancel: %#v %v", cancelled, err)
	}
	if _, err := database.ActivateAIInvocationRuntime(ctx, run.Execution.RunID, "vercel-workflow", "late-worker"); err == nil {
		t.Fatal("cancelled admission activated")
	}
	if err := database.RevokeSDKTarget(ctx, user, pin.TargetID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AdmitManualRoutine(ctx, user, id, uuid.NewString(), 1, json.RawMessage(`{}`)); err == nil {
		t.Fatal("revoked target admitted")
	}
}
