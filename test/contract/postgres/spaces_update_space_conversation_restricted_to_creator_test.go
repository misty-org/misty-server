package db

import (
	"context"
	"errors"
	. "github.com/misty-org/misty-server/internal/platform/db"
	"testing"
)

func TestUpdateSpaceConversationRestrictedToCreator(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("update-owner@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser(owner) error = %v", err)
	}
	member, err := database.CreateUser("update-member@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser(member) error = %v", err)
	}
	outsider, err := database.CreateUser("update-outsider@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser(outsider) error = %v", err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Conversation updates")
	for _, invited := range []*User{member, outsider} {
		invite, inviteErr := database.TestingInviteToSpace(ctx, owner.ID, space.ID, invited.Email)
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

	// Members can read and write messages, but only the Space's owner renames.
	if _, err := database.UpdateSpaceConversation(ctx, member.ID, space.ID, conversation.ID, "Hijacked name"); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("UpdateSpaceConversation(member) error = %v, want ErrSpaceForbidden", err)
	}
	updated, err := database.UpdateSpaceConversation(ctx, owner.ID, space.ID, conversation.ID, "Renamed crew")
	if err != nil {
		t.Fatalf("UpdateSpaceConversation(owner) error = %v", err)
	}
	if updated.Title != "Renamed crew" || len(updated.Participants) != 3 {
		t.Fatalf("updated conversation = %#v, want renamed with everyone in the Space", updated)
	}
	everyone, err := database.SpaceConversations(ctx, outsider.ID, space.ID)
	if err != nil || len(everyone) != 1 || everyone[0].ID != conversation.ID {
		t.Fatalf("SpaceConversations(another member) = %#v, %v", everyone, err)
	}

	// A DM can't be changed, by anyone: not its participants and not the Space's owner.
	dm, err := database.DirectMemberConversation(ctx, member.ID, space.ID, outsider.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{owner.ID, member.ID} {
		if _, err := database.UpdateSpaceConversation(ctx, user, space.ID, dm.ID, "Hijacked name"); !errors.Is(err, ErrSpaceForbidden) {
			t.Fatalf("UpdateSpaceConversation(DM) error = %v, want ErrSpaceForbidden", err)
		}
	}
	if _, err := database.SpaceConversationMessages(ctx, owner.ID, space.ID, dm.ID, 0, 20); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("SpaceConversationMessages(owner outside the DM) error = %v, want ErrSpaceForbidden", err)
	}
}

func containsSpaceEvent(events []SpaceEvent, eventType, entityID string) bool {
	for _, event := range events {
		if event.EventType == eventType && event.EntityID == entityID {
			return true
		}
	}
	return false
}
