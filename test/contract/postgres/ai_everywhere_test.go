package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAIEverywhereIsolationIdempotencyAndImmediateDisable(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("AI Owner", "ai-everywhere-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("AI Other", "ai-everywhere-other@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	ownerSpace, err := database.CreateSpace(ctx, owner.ID, "Owner AI Space")
	if err != nil {
		t.Fatal(err)
	}
	otherSpace, err := database.CreateSpace(ctx, other.ID, "Other AI Space")
	if err != nil {
		t.Fatal(err)
	}
	ownerTask, err := database.CreateSpaceTask(ctx, owner.ID, SpaceTask{
		SpaceID: ownerSpace.ID, Title: "Alpha confidential launch", Notes: "owner-only retrieval marker",
		Status: "todo", Priority: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateSpaceTask(ctx, other.ID, SpaceTask{
		SpaceID: otherSpace.ID, Title: "Beta confidential launch", Notes: "other-only retrieval marker",
		Status: "todo", Priority: "high",
	}); err != nil {
		t.Fatal(err)
	}

	ownerHits, err := database.SearchAIRetrieval(ctx, owner.ID, "confidential launch", nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !containsAISource(ownerHits, ownerTask.ID) || containsAIText(ownerHits, "other-only retrieval marker") {
		t.Fatalf("permission-first owner retrieval leaked or omitted content: %#v", ownerHits)
	}
	otherHits, err := database.SearchAIRetrieval(ctx, other.ID, "confidential launch", nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if containsAISource(otherHits, ownerTask.ID) || containsAIText(otherHits, "owner-only retrieval marker") {
		t.Fatalf("cross-account retrieval leaked content: %#v", otherHits)
	}
	recent, err := database.RecentAIRetrieval(ctx, other.ID, 20)
	if err != nil || containsAISource(recent, ownerTask.ID) || containsAIText(recent, "owner-only retrieval marker") {
		t.Fatalf("permission-first recap retrieval leaked content: %#v, %v", recent, err)
	}
	if _, err := database.ArchiveSpaceTask(ctx, owner.ID, ownerSpace.ID, ownerTask.ID, ownerTask.Version); err != nil {
		t.Fatal(err)
	}
	ownerHits, err = database.SearchAIRetrieval(ctx, owner.ID, "owner-only retrieval marker", nil, 20)
	if err != nil || containsAISource(ownerHits, ownerTask.ID) {
		t.Fatalf("archived source remained retrievable: %#v, %v", ownerHits, err)
	}

	now := time.Now().UTC()
	request := AIInvocationRecord{
		ID: "invocation_owner_first", UserID: owner.ID, SurfaceID: "notes", Mode: "quick",
		Trigger: "selection", State: "queued", IdempotencyKey: "same-retry-key",
		RequestPayload: json.RawMessage(`{"prompt":"first"}`), ExpiresAt: now.Add(time.Hour),
	}
	first, created, err := database.CreateAIInvocationRecord(ctx, request)
	if err != nil || !created {
		t.Fatalf("first invocation = %#v, created=%v, err=%v", first, created, err)
	}
	request.ID = "invocation_owner_retry"
	replayed, created, err := database.CreateAIInvocationRecord(ctx, request)
	if err != nil || created || replayed.ID != first.ID {
		t.Fatalf("retry duplicated invocation: %#v, created=%v, err=%v", replayed, created, err)
	}
	request.ID, request.UserID = "invocation_other", other.ID
	isolated, created, err := database.CreateAIInvocationRecord(ctx, request)
	if err != nil || !created || isolated.ID == first.ID {
		t.Fatalf("idempotency crossed accounts: %#v, created=%v, err=%v", isolated, created, err)
	}

	recap, err := database.UpsertAIRecap(ctx, owner.ID, AIRecap{
		SurfaceID: "home", Enabled: true, Cadence: "daily", LocalTime: "08:00",
		Weekday: 1, Timezone: "UTC", Prompt: "Summarize recent work",
	}, now)
	if err != nil || recap.NextRunAt == nil {
		t.Fatalf("recap was not durably scheduled: %#v, %v", recap, err)
	}
	if err := database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE ai_recaps SET next_run_at=$1 WHERE user_id=$2 AND surface_id='home'`, now.Add(-time.Minute), owner.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	due, err := database.ClaimDueAIRecaps(ctx, now, 10)
	if err != nil || len(due) != 1 || due[0].UserID != owner.ID {
		t.Fatalf("recap claim crossed an owner boundary or missed the due row: %#v, %v", due, err)
	}
	if err := database.CompleteAIRecap(ctx, due[0], "", "Grounded briefing", json.RawMessage(`[]`), nil, now); err != nil {
		t.Fatal(err)
	}
	ownerRecaps, err := database.AIRecaps(ctx, owner.ID)
	if err != nil || len(ownerRecaps) != 1 || ownerRecaps[0].LastResult != "Grounded briefing" {
		t.Fatalf("completed recap was not delivered: %#v, %v", ownerRecaps, err)
	}
	otherRecaps, err := database.AIRecaps(ctx, other.ID)
	if err != nil || len(otherRecaps) != 0 {
		t.Fatalf("personal recap crossed accounts: %#v, %v", otherRecaps, err)
	}

	settings, err := database.UpdateAISettings(ctx, owner.ID, false, 30, true, true)
	if err != nil || settings.Enabled || settings.PurgeState != "queued" {
		t.Fatalf("disable did not enter purge queue: %#v, %v", settings, err)
	}
	available, err := database.AIActionAvailable(ctx, owner.ID, "notes", "ask", "automatic")
	if err != nil || available {
		t.Fatalf("disabled AI accepted new work: available=%v err=%v", available, err)
	}
	request.ID, request.UserID, request.IdempotencyKey = "invocation_after_disable", owner.ID, "after-disable"
	if _, _, err := database.CreateAIInvocationRecord(ctx, request); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("disabled AI won the invocation start race: %v", err)
	}
	if _, err := database.AIInvocationByID(ctx, owner.ID, first.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("unaccepted transient survived immediate disable: %v", err)
	}
	ownerRecaps, err = database.AIRecaps(ctx, owner.ID)
	if err != nil || len(ownerRecaps) != 0 {
		t.Fatalf("AI disable did not purge recurring briefings: %#v, %v", ownerRecaps, err)
	}
	if _, err := database.ProcessAICleanupJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	settings, _, err = database.AISettings(ctx, owner.ID)
	if err != nil || settings.PurgeState != "verified" {
		t.Fatalf("cleanup was not verified: %#v, %v", settings, err)
	}
}

func TestAIConversationTurnsExposeOriginalPromptAndDurableOutcome(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Companion history owner", "companion-history-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	conversationID := "conversation_11111111-1111-4111-8111-111111111111"
	invocationID := "invocation_22222222-2222-4222-8222-222222222222"
	state := json.RawMessage(`{"id":"conversation_11111111-1111-4111-8111-111111111111","userId":"` + owner.ID + `","billingUserId":"` + owner.ID + `","mode":"ask","agentTier":"tier-low","createdAt":"` + now.Format(time.RFC3339Nano) + `","updatedAt":"` + now.Format(time.RFC3339Nano) + `"}`)
	if err := database.CreateAgentSession(ctx, conversationID, owner.ID, state, now.Add(time.Hour), now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := database.BindCompanionConversation(ctx, owner.ID, conversationID, "", "", "test-model", "home", "/home", "private"); err != nil {
		t.Fatal(err)
	}
	if _, created, err := database.CreateAIInvocationRecord(ctx, AIInvocationRecord{
		ID: invocationID, UserID: owner.ID, ConversationID: conversationID,
		SurfaceID: "home", Mode: "companion", Trigger: "message", State: "queued",
		IdempotencyKey: "companion-history-original-prompt", RequestPayload: json.RawMessage(`{"prompt":"hello"}`),
		ExpiresAt: now.Add(time.Hour),
	}); err != nil || !created {
		t.Fatalf("create invocation: created=%v err=%v", created, err)
	}
	status := json.RawMessage(`{"type":"assistant.status","text":"Checking your Space…"}`)
	if err := database.AppendAIInvocationEvent(ctx, owner.ID, invocationID, 1, "assistant.status", status, "running"); err != nil {
		t.Fatal(err)
	}
	failure := json.RawMessage(`{"type":"invocation.failed","state":"failed","error":"Weekly AI pool reached."}`)
	if err := database.AppendAIInvocationEvent(ctx, owner.ID, invocationID, 2, "invocation.failed", failure, "failed"); err != nil {
		t.Fatal(err)
	}
	turns, err := database.AIConversationTurns(ctx, owner.ID, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].Prompt != "hello" || turns[0].Status != "Checking your Space…" || turns[0].Failure != "Weekly AI pool reached." || turns[0].State != "failed" {
		t.Fatalf("unexpected companion turns: %#v", turns)
	}
	other, err := database.CreateUser("Other history owner", "companion-history-other@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	otherTurns, err := database.AIConversationTurns(ctx, other.ID, conversationID)
	if err != nil || len(otherTurns) != 0 {
		t.Fatalf("companion history crossed accounts: %#v, %v", otherTurns, err)
	}
}

func containsAISource(hits []AIRetrievalHit, sourceID string) bool {
	for _, hit := range hits {
		if hit.SourceID == sourceID {
			return true
		}
	}
	return false
}

func containsAIText(hits []AIRetrievalHit, value string) bool {
	for _, hit := range hits {
		if strings.Contains(hit.Content, value) {
			return true
		}
	}
	return false
}
