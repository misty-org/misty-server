package db

import (
	"context"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestUpdateSpaceConversationRestrictedToCreator(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Update Owner", "update-owner@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser(owner) error = %v", err)
	}
	member, err := database.CreateUser("Update Member", "update-member@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser(member) error = %v", err)
	}
	outsider, err := database.CreateUser("Update Outsider", "update-outsider@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser(outsider) error = %v", err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Conversation updates")
	for _, invited := range []*User{member, outsider} {
		invite, inviteErr := database.InviteToSpace(ctx, owner.ID, space.ID, invited.Email)
		if inviteErr != nil {
			t.Fatalf("InviteToSpace(%s) error = %v", invited.Email, inviteErr)
		}
		if _, inviteErr = database.RespondToSpaceInvite(ctx, invited.ID, invite.ID, true); inviteErr != nil {
			t.Fatalf("RespondToSpaceInvite(%s) error = %v", invited.Email, inviteErr)
		}
	}

	conversation, err := database.CreateSpaceConversation(ctx, owner.ID, space.ID, "Launch crew", []SpaceActorRef{{Kind: "person", UserID: member.ID}})
	if err != nil {
		t.Fatalf("CreateSpaceConversation() error = %v", err)
	}

	// A non-creator member can read and write messages, but cannot rename the
	// conversation or change who's in it.
	if _, err := database.UpdateSpaceConversation(ctx, member.ID, space.ID, conversation.ID, "Hijacked name", []SpaceActorRef{{Kind: "person", UserID: owner.ID}, {Kind: "person", UserID: member.ID}}); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("UpdateSpaceConversation(non-creator member) error = %v, want ErrSpaceForbidden", err)
	}

	// An outsider who isn't even in the conversation is rejected too.
	if _, err := database.UpdateSpaceConversation(ctx, outsider.ID, space.ID, conversation.ID, "Hijacked name", []SpaceActorRef{{Kind: "person", UserID: owner.ID}, {Kind: "person", UserID: outsider.ID}}); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("UpdateSpaceConversation(outsider not in conversation) error = %v, want ErrSpaceForbidden", err)
	}

	// The creator can rename the conversation and add a new member.
	updated, err := database.UpdateSpaceConversation(ctx, owner.ID, space.ID, conversation.ID, "Renamed crew", []SpaceActorRef{{Kind: "person", UserID: member.ID}, {Kind: "person", UserID: outsider.ID}})
	if err != nil {
		t.Fatalf("UpdateSpaceConversation(creator, add member) error = %v", err)
	}
	if updated.Title != "Renamed crew" || len(updated.Participants) != 3 {
		t.Fatalf("updated conversation = %#v, want renamed with 3 members", updated)
	}
	outsiderConversations, err := database.SpaceConversations(ctx, outsider.ID, space.ID)
	if err != nil || len(outsiderConversations) != 1 || outsiderConversations[0].ID != conversation.ID {
		t.Fatalf("SpaceConversations(newly added member) = %#v, %v", outsiderConversations, err)
	}

	// The creator can also remove a member; that member immediately loses
	// access to the conversation and its messages.
	if _, err := database.UpdateSpaceConversation(ctx, owner.ID, space.ID, conversation.ID, "Renamed crew", []SpaceActorRef{{Kind: "person", UserID: outsider.ID}}); err != nil {
		t.Fatalf("UpdateSpaceConversation(creator, remove member) error = %v", err)
	}
	memberConversationsAfterRemoval, err := database.SpaceConversations(ctx, member.ID, space.ID)
	if err != nil || len(memberConversationsAfterRemoval) != 0 {
		t.Fatalf("SpaceConversations(removed member) = %#v, %v, want none", memberConversationsAfterRemoval, err)
	}
	if _, err := database.SpaceConversationMessages(ctx, member.ID, space.ID, conversation.ID, 0, 20); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("SpaceConversationMessages(removed member) error = %v, want ErrSpaceForbidden", err)
	}
}

func containsInboxMessage(items []SpaceInboxItem, messageID string) bool {
	for _, item := range items {
		if item.MessageID == messageID {
			return true
		}
	}
	return false
}

func containsSpaceEvent(events []SpaceEvent, eventType, entityID string) bool {
	for _, event := range events {
		if event.EventType == eventType && event.EntityID == entityID {
			return true
		}
	}
	return false
}
