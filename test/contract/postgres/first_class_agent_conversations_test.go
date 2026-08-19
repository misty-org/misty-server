package db

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAgentDirectConversationsAreCanonicalPerMemberAndSpace(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Agent owner", "canonical-agent-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Agent member", "canonical-agent-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Canonical conversations")
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	agent, err := database.CreatePersonalAgent(ctx, owner.ID, PersonalAgent{
		Name: "Researcher", Instructions: "Answer from the current conversation.",
		ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := database.DirectAgentConversation(ctx, owner.ID, space.ID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	again, err := database.DirectAgentConversation(ctx, owner.ID, space.ID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != again.ID || first.Kind != "direct" || len(first.Participants) != 2 {
		t.Fatalf("canonical direct conversation mismatch: %#v %#v", first, again)
	}
	if _, err := database.DirectAgentConversation(ctx, member.ID, space.ID, agent.ID); err == nil {
		t.Fatal("non-creator opened a direct Agent conversation")
	}
	group, err := database.CreateSpaceConversation(ctx, owner.ID, space.ID, "Research group", []SpaceActorRef{
		{Kind: "person", UserID: member.ID}, {Kind: "agent", AgentID: agent.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(group.Participants) != 3 {
		t.Fatalf("group participants = %#v", group.Participants)
	}
}

func TestAgentDirectConversationWorksInDefaultMistySpace(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser(
		"Default companion owner",
		"default-companion-owner@example.com",
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureDefaultSpace(ctx, owner.ID); err != nil {
		t.Fatal(err)
	}
	space := requireDefaultMistySpace(t, database, ctx, owner.ID)
	agent, err := database.CreatePersonalAgent(ctx, owner.ID, PersonalAgent{
		Name:      "Welcome companion",
		ModelMode: "pinned",
		ModelID:   "google/gemini-2.5-flash-lite",
	})
	if err != nil {
		t.Fatal(err)
	}

	conversation, err := database.DirectAgentConversation(ctx, owner.ID, space.ID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if conversation.Kind != "direct" || conversation.DirectUserID != owner.ID ||
		conversation.DirectAgentID != agent.ID {
		t.Fatalf("default companion conversation = %#v", conversation)
	}
}

func TestDeletingAgentDirectConversationClosesAndRecreatesIt(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Delete direct owner", "delete-agent-direct@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Delete direct conversations")
	agent, err := database.CreatePersonalAgent(ctx, owner.ID, PersonalAgent{
		Name: "Closable", Instructions: "Help with the current conversation.",
		ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite",
	})
	if err != nil {
		t.Fatal(err)
	}
	direct, err := database.DirectAgentConversation(ctx, owner.ID, space.ID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := database.CreatePersonalAgentSpaceRun(
		ctx, owner.ID, space.ID, agent.ID, direct.ID, "direct", "direct",
		json.RawMessage(`{"prompt":"hello"}`), json.RawMessage(`{"allowed_tools":[]}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteOrClearSpaceConversation(ctx, owner.ID, space.ID, direct.ID); err != nil {
		t.Fatalf("delete direct conversation: %v", err)
	}
	if _, err := database.SpaceRun(ctx, owner.ID, run.ID); err == nil {
		t.Fatalf("conversation-scoped run %q survived deletion", run.ID)
	}
	events, _, err := database.SpaceEventsAfter(ctx, owner.ID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	deleteEventVisible := false
	for _, event := range events {
		if event.EventType == "conversation.deleted" && event.EntityID == direct.ID {
			deleteEventVisible = true
		}
	}
	if !deleteEventVisible {
		t.Fatalf("conversation.deleted event for %q was not visible to its member", direct.ID)
	}
	conversations, err := database.SpaceConversations(ctx, owner.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, conversation := range conversations {
		if conversation.ID == direct.ID {
			t.Fatalf("deleted direct conversation remained visible: %#v", conversation)
		}
	}
	recreated, err := database.DirectAgentConversation(ctx, owner.ID, space.ID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recreated.ID == direct.ID {
		t.Fatalf("deleted conversation %q was reused", direct.ID)
	}
}

// An Agent member of a conversation has a NULL user_id, and only people have an
// inbox. Fanning the reply out to every member row without that distinction
// failed the whole insert, so the Agent's answer was silently lost after the
// model had already run.
func TestAgentRepliesPostIntoConversationsThatHaveAgentMembers(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Reply owner", "agent-reply-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Agent replies")
	agent, err := database.CreatePersonalAgent(ctx, owner.ID, PersonalAgent{
		Name: "Responder", Instructions: "Answer from the current conversation.",
		ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite",
	})
	if err != nil {
		t.Fatal(err)
	}
	direct, err := database.DirectAgentConversation(ctx, owner.ID, space.ID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := database.CreateSpaceConversationAgentMessage(ctx, owner.ID, space.ID, direct.ID, agent.ID, "On it.")
	if err != nil {
		t.Fatalf("agent reply into a direct conversation: %v", err)
	}
	if reply.SenderKind != "agent" || reply.SenderAgentID != agent.ID {
		t.Fatalf("reply sender = %#v", reply.Sender)
	}
	messages, err := database.SpaceConversationMessages(ctx, owner.ID, space.ID, direct.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range messages {
		if message.ID == reply.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("agent reply %q is missing from the conversation: %#v", reply.ID, messages)
	}
}
