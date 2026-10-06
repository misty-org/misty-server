package db

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)


import ()

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
	if err := database.BindAskSurfaceConversation(ctx, owner.ID, conversationID, "", "test-model", "home", "/home", "private"); err != nil {
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
	if err := database.TestingAppendAIInvocationEvent(ctx, owner.ID, invocationID, 1, "assistant.status", status, "running"); err != nil {
		t.Fatal(err)
	}
	failure := json.RawMessage(`{"type":"invocation.failed","state":"failed","error":"Weekly AI pool reached."}`)
	if err := database.TestingAppendAIInvocationEvent(ctx, owner.ID, invocationID, 2, "invocation.failed", failure, "failed"); err != nil {
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
