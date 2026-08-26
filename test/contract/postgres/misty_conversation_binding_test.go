package db

import (
	"context"
	"errors"
	"testing"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestMistyConversationSpaceBindingIsImmutableAndOwnerIsolated(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Binding Owner", "binding-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Binding Other", "binding-other@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	first, err := database.CreateSpace(ctx, owner.ID, "First bound Space")
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.CreateSpace(ctx, owner.ID, "Second bound Space")
	if err != nil {
		t.Fatal(err)
	}
	conversationID, err := database.CreateAIConversation(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.BindMistyConversationSpace(ctx, owner.ID, conversationID, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.BindMistyConversationSpace(ctx, owner.ID, conversationID, first.ID); err != nil {
		t.Fatalf("same-Space bind should be idempotent: %v", err)
	}
	if err := database.BindMistyConversationSpace(ctx, owner.ID, conversationID, second.ID); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("conversation was rebound to another Space: %v", err)
	}
	bound, err := database.AgentConversationIdentity(ctx, owner.ID, conversationID)
	if err != nil || bound.SpaceID != first.ID {
		t.Fatalf("bound identity = %#v, %v", bound, err)
	}
	if err := database.BindMistyConversationSpace(ctx, other.ID, conversationID, second.ID); !errors.Is(err, serveragent.ErrPersistedSessionNotFound) {
		t.Fatalf("another account could probe or rebind the conversation: %v", err)
	}
}
