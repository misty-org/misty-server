package db

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestConversationNotesOnlyMoveForwardAndStayWithTheirOwner(t *testing.T) {
	database := openTestDatabase(t)
	ctx := t.Context()
	owner, err := database.CreateUser("Notes Owner", "conversation-notes-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Notes Other", "conversation-notes-other@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	conversationID := "conversation_" + uuid.NewString()
	if err := database.CreateAgentSession(ctx, conversationID, owner.ID, json.RawMessage(`{}`), now.Add(time.Hour), now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if notes, err := database.AIConversationSummary(ctx, owner.ID, conversationID); err != nil || notes != nil {
		t.Fatalf("notes before any summary = %+v %v", notes, err)
	}
	save := func(user string, turns int, text string) error {
		return database.SaveAIConversationSummary(ctx, user, AIConversationSummary{
			ConversationID: conversationID, ThroughInvocationID: "invocation_" + text, ThroughCreatedAt: now,
			SummarizedTurns: turns, Summary: text, Model: "google/gemini-2.5-flash",
		})
	}
	if err := save(owner.ID, 10, "ten"); err != nil {
		t.Fatal(err)
	}
	if err := save(owner.ID, 6, "six"); err != nil {
		t.Fatal(err)
	}
	if err := save(owner.ID, 14, "fourteen"); err != nil {
		t.Fatal(err)
	}
	notes, err := database.AIConversationSummary(ctx, owner.ID, conversationID)
	if err != nil || notes == nil || notes.Summary != "fourteen" || notes.SummarizedTurns != 14 || notes.ThroughInvocationID != "invocation_fourteen" {
		t.Fatalf("a stale summary replaced a newer one: %+v %v", notes, err)
	}
	_ = save(other.ID, 20, "intruder")
	if notes, _ := database.AIConversationSummary(ctx, other.ID, conversationID); notes != nil {
		t.Fatal("another account read the notes")
	}
	if notes, _ := database.AIConversationSummary(ctx, owner.ID, conversationID); notes.Summary != "fourteen" {
		t.Fatal("another account overwrote the notes")
	}
	if err := database.SaveAIConversationSummary(ctx, owner.ID, AIConversationSummary{ConversationID: conversationID, ThroughInvocationID: "x", SummarizedTurns: 0, Summary: "bad"}); err == nil {
		t.Fatal("invalid notes accepted")
	}
}
