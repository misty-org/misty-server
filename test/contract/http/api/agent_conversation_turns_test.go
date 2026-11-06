package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestConversationTurnsCanBeDroppedMovedAndLedgerSaved(t *testing.T) {
	f := newOnServerFixture(t)
	ctx := context.Background()
	from, err := f.database.CreatePersonalAgentConversation(ctx, f.owner.ID, "", f.agentID)
	if err != nil {
		t.Fatal(err)
	}
	to, err := f.database.CreatePersonalAgentConversation(ctx, f.owner.ID, "", f.agentID)
	if err != nil {
		t.Fatal(err)
	}
	first := f.finishedRun(t, from, "First\n\n<!--misty-ledger:[{\"id\":\"a\",\"requirement\":\"r\",\"state\":\"pending\"}]-->")
	second := f.finishedRun(t, from, "Second")
	third := f.finishedRun(t, from, "Third")

	// Someone else can't touch them.
	if rec := f.request(t, http.MethodPost, "/misty/conversations/"+from+"/turns", f.member.ID, map[string]any{"action": "detach", "invocation_ids": []string{third.ID}}); rec.Code != http.StatusNotFound {
		t.Fatalf("another person's detach = %d", rec.Code)
	}
	// Sending a prompt again drops it and what came after from the next run's history.
	if rec := f.request(t, http.MethodPost, "/misty/conversations/"+from+"/turns", f.owner.ID, map[string]any{"action": "detach", "invocation_ids": []string{third.ID}}); rec.Code != http.StatusNoContent {
		t.Fatalf("detach = %d, %s", rec.Code, rec.Body)
	}
	if rec := f.request(t, http.MethodPost, "/misty/conversations/"+from+"/turns", f.owner.ID, map[string]any{"action": "move", "invocation_ids": []string{second.ID}, "to_conversation_id": to}); rec.Code != http.StatusNoContent {
		t.Fatalf("move = %d, %s", rec.Code, rec.Body)
	}
	turns, err := f.database.AIConversationTurns(ctx, f.owner.ID, from)
	if err != nil || len(turns) != 1 || turns[0].InvocationID != first.ID {
		t.Fatalf("turns left = %#v, %v", turns, err)
	}
	moved, err := f.database.AIConversationTurns(ctx, f.owner.ID, to)
	if err != nil || len(moved) != 1 || moved[0].InvocationID != second.ID {
		t.Fatalf("moved turns = %#v, %v", moved, err)
	}
	// An edited ledger stays with the reply, and can't change what it said.
	rows := []map[string]any{{"id": "a", "requirement": "r", "state": "skipped"}}
	if rec := f.request(t, http.MethodPost, "/misty/conversations/"+from+"/ledger", f.owner.ID, map[string]any{"invocation_id": first.ID, "rows": rows}); rec.Code != http.StatusNoContent {
		t.Fatalf("ledger = %d, %s", rec.Code, rec.Body)
	}
	turns, _ = f.database.AIConversationTurns(ctx, f.owner.ID, from)
	if !strings.HasPrefix(turns[0].Reply, "First") || !strings.Contains(turns[0].Reply, `"skipped"`) || strings.Contains(turns[0].Reply, `"pending"`) {
		t.Fatalf("reply with its ledger = %q", turns[0].Reply)
	}
}

// TestLeavingASpaceTakesThePersonsConversationsThereButNoOneElses checks the
// private layer: their chats in that Space go with them, and those elsewhere stay.
func TestLeavingASpaceTakesThePersonsConversationsThereButNoOneElses(t *testing.T) {
	f := newOnServerFixture(t)
	ctx := context.Background()
	inSpace, err := f.database.CreatePersonalAgentConversation(ctx, f.owner.ID, f.space.ID, f.agentID)
	if err != nil {
		t.Fatal(err)
	}
	elsewhere, err := f.database.CreatePersonalAgentConversation(ctx, f.owner.ID, "", f.agentID)
	if err != nil {
		t.Fatal(err)
	}
	run := f.finishedRun(t, inSpace, "Reply")
	if err := f.database.LeaveSpace(ctx, f.owner.ID, f.space.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.scalar(t, `SELECT string_agg(id,',') FROM misty_ask_conversations WHERE id=ANY(ARRAY[$1,$2])`, inSpace, elsewhere); got != elsewhere {
		t.Fatalf("conversations left = %q", got)
	}
	if got := f.scalar(t, `SELECT count(*)::text FROM ai_invocations WHERE id=$1`, run.ID); got != "0" {
		t.Fatal("a turn stayed after leaving the Space")
	}
}
