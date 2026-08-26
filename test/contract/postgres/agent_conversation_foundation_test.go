package db

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestMistyConversationFocusCarriesTaskTargetAcrossTurns(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Focused Task Owner", "focused-task-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Focused Tasks")
	if err != nil {
		t.Fatal(err)
	}
	conversationID := "conversation-focused-task"
	createdRaw, err := api.TestingExecuteAIInvocationSpaceToolWithConversation(
		ctx, database, owner.ID, space.ID, conversationID, "invocation_create_focus",
		"Create a task called Finish the laundry", "tasks.create", json.RawMessage(`{"title":"Finish the laundry"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var created SpaceTask
	if err := json.Unmarshal(createdRaw, &created); err != nil {
		t.Fatal(err)
	}
	focus, err := database.AIConversationFocusByKind(ctx, owner.ID, conversationID, space.ID, "task")
	if err != nil || focus.EntityID != created.ID || focus.Label != created.Title || focus.SourceTool != "tasks.create" {
		t.Fatalf("recorded conversation focus = %#v, task = %#v, err = %v", focus, created, err)
	}

	followup := "Add it to the description. Then, actually, can you assign it to me instead?"
	names, err := api.TestingResolveAIInvocationSpaceToolNamesWithConversation(ctx, database, owner.ID, space.ID, conversationID, "invocation_update_focus", followup)
	if err != nil || !slices.Contains(names, "tasks.update") {
		t.Fatalf("focused follow-up tools = %v, err = %v", names, err)
	}
	notes := "Remember not to mix outside and inside clothes."
	updatedRaw, err := api.TestingExecuteAIInvocationSpaceToolWithConversation(
		ctx, database, owner.ID, space.ID, conversationID, "invocation_update_focus", followup, "tasks.update",
		json.RawMessage(`{"id":"`+created.ID+`","notes":"`+notes+`","assigneeUserId":"`+owner.ID+`"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var updated SpaceTask
	if json.Unmarshal(updatedRaw, &updated) != nil || updated.ID != created.ID || updated.Notes != notes || updated.AssigneeUserID != owner.ID {
		t.Fatalf("focused task update = %#v", updated)
	}

	if err := database.ClearAIConversationFocus(ctx, owner.ID, conversationID, space.ID, "task"); err != nil {
		t.Fatal(err)
	}
	names, err = api.TestingResolveAIInvocationSpaceToolNamesWithConversation(ctx, database, owner.ID, space.ID, conversationID, "invocation_no_focus", followup)
	if err != nil || slices.Contains(names, "tasks.update") {
		t.Fatalf("ungrounded follow-up tools = %v, err = %v", names, err)
	}
	if err := database.UpsertAIConversationPendingAction(ctx, AIConversationPendingAction{
		UserID: owner.ID, ConversationID: conversationID, SpaceID: space.ID, Intent: "tasks.update",
		Question: "Which task would you like me to update?", OriginalPrompt: "Can you assign it to me?",
	}); err != nil {
		t.Fatal(err)
	}
	answer := "The Finish the laundry task"
	names, err = api.TestingResolveAIInvocationSpaceToolNamesWithConversation(ctx, database, owner.ID, space.ID, conversationID, "invocation_answer_focus", answer)
	if err != nil || !slices.Contains(names, "tasks.update") {
		t.Fatalf("clarification answer tools = %v, err = %v", names, err)
	}
	if _, err := api.TestingExecuteAIInvocationSpaceToolWithConversation(ctx, database, owner.ID, space.ID, conversationID, "invocation_answer_focus", answer, "tasks.query", json.RawMessage(`{"query":"Finish the laundry"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := api.TestingExecuteAIInvocationSpaceToolWithConversation(ctx, database, owner.ID, space.ID, conversationID, "invocation_answer_focus", answer, "tasks.update", json.RawMessage(`{"id":"`+created.ID+`","notes":"Clarification continued successfully."}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AIConversationPendingAction(ctx, owner.ID, conversationID, space.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("completed pending action still exists: %v", err)
	}

	if err := database.ClearAIConversationFocus(ctx, owner.ID, conversationID, space.ID, "task"); err != nil {
		t.Fatal(err)
	}
	references := json.RawMessage(`[{"kind":"task","id":"` + created.ID + `","title":"client supplied title","privacy":"shared","space_id":"` + space.ID + `"}]`)
	if err := api.TestingRecordAIConversationFocusFromUIContext(ctx, database, owner.ID, conversationID, space.ID, references); err != nil {
		t.Fatal(err)
	}
	uiFocus, err := database.AIConversationFocusByKind(ctx, owner.ID, conversationID, space.ID, "task")
	if err != nil || uiFocus.EntityID != created.ID || uiFocus.Label != created.Title || uiFocus.SourceTool != "ui.context" {
		t.Fatalf("UI context focus = %#v, err = %v", uiFocus, err)
	}
	note, err := database.CreateSpaceNoteWithAudience(ctx, owner.ID, space.ID, "Laundry notes", SpaceResourceAudience{Kind: SpaceAudienceSpace}, "Sort the clothes")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertAIConversationFocus(ctx, AIConversationFocus{
		UserID: owner.ID, ConversationID: conversationID, SpaceID: space.ID,
		EntityKind: "note", EntityID: note.ID, Label: note.TitleProjection, SourceTool: "notes.create",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertAIConversationPendingAction(ctx, AIConversationPendingAction{
		UserID: owner.ID, ConversationID: conversationID, SpaceID: space.ID, Intent: "clarify",
		Question: "Which item do you mean?", CandidateIntents: json.RawMessage(`["tasks.update","notes.update"]`),
	}); err != nil {
		t.Fatal(err)
	}
	names, err = api.TestingResolveAIInvocationSpaceToolNamesWithConversation(ctx, database, owner.ID, space.ID, conversationID, "invocation_cross_tool_answer", "The note")
	if err != nil || !slices.Contains(names, "notes.update") || slices.Contains(names, "tasks.update") {
		t.Fatalf("cross-tool clarification answer tools = %v, err = %v", names, err)
	}
}

func TestMistyConversationFocusCannotBypassChangedSpacePermissions(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Focus Permission Owner", "focus-permission-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Focus Permission Member", "focus-permission-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Permission Focus")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	task, err := database.CreateSpaceTask(ctx, owner.ID, SpaceTask{
		SpaceID: space.ID, Title: "Protected task", Status: "todo", Priority: "medium",
		SourceRefs: json.RawMessage(`[]`), CreatedByUserID: owner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversationID := "conversation-permission-focus"
	if _, err := api.TestingExecuteAIInvocationSpaceToolWithConversation(
		ctx, database, member.ID, space.ID, conversationID, "invocation_focus_query", "Find the protected task",
		"tasks.query", json.RawMessage(`{"query":"Protected task"}`),
	); err != nil {
		t.Fatal(err)
	}
	focus, err := database.AIConversationFocusByKind(ctx, member.ID, conversationID, space.ID, "task")
	if err != nil || focus.EntityID != task.ID {
		t.Fatalf("member focus = %#v, err = %v", focus, err)
	}
	if err := database.SetSpaceMemberPermission(ctx, owner.ID, space.ID, member.ID, PermissionTasksManage, "deny"); err != nil {
		t.Fatal(err)
	}
	names, err := api.TestingResolveAIInvocationSpaceToolNamesWithConversation(
		ctx, database, member.ID, space.ID, conversationID, "invocation_focus_denied", "Mark it done",
	)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(names, "tasks.update") {
		t.Fatalf("revoked permission still exposed tasks.update: %v", names)
	}
	if _, err := database.AIConversationFocusByKind(ctx, owner.ID, conversationID, space.ID, "task"); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("another user observed member focus: %v", err)
	}
}
