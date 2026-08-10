package db

import (
	"context"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestCanonicalMistySpaceUsesPrivateSupportConversations(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	first, err := database.CreateUser("First User", "default-first@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.CreateUser("Second User", "default-second@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureDefaultSpace(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureDefaultSpace(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	firstSpace := requireDefaultMistySpace(t, database, ctx, first.ID)
	secondSpace := requireDefaultMistySpace(t, database, ctx, second.ID)
	if firstSpace.ID != secondSpace.ID || firstSpace.Kind != "misty" || secondSpace.Kind != "misty" {
		t.Fatalf("accounts did not receive the canonical Misty Space: %#v / %#v", firstSpace, secondSpace)
	}
	for _, space := range []Space{firstSpace, secondSpace} {
		if space.Role != "member" || space.IsShared {
			t.Fatalf("canonical Space projection is not private: %#v", space)
		}
		for _, permission := range []string{
			PermissionMessagesRead, PermissionMessagesWrite, PermissionAttachmentUpload,
		} {
			if !space.Permissions[permission] {
				t.Fatalf("canonical Space lacks %s: %#v", permission, space.Permissions)
			}
		}
		for _, permission := range []string{
			PermissionLibraryView, PermissionTasksView, PermissionAgentsRun,
			PermissionSpaceInvite, PermissionSpaceRename, PermissionSpaceTransfer,
			PermissionSpaceDelete, PermissionSpaceLeave,
		} {
			if space.Permissions[permission] {
				t.Fatalf("ordinary member unexpectedly has %s: %#v", permission, space.Permissions)
			}
		}
	}
	if _, err := database.RenameSpace(ctx, first.ID, firstSpace.ID, "Personal home"); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("RenameSpace(canonical Misty) = %v, want ErrSpaceForbidden", err)
	}
	if _, err := database.InviteToSpace(ctx, first.ID, firstSpace.ID, second.Email); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("InviteToSpace(canonical Misty) = %v, want ErrSpaceForbidden", err)
	}
	if _, _, err := database.CreateSpaceMessage(ctx, first.ID, firstSpace.ID, []MessageSpan{{Type: "text", Text: "not private"}}, nil); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("CreateSpaceMessage(canonical Everyone) = %v, want ErrSpaceForbidden", err)
	}

	firstConversations, err := database.SpaceConversations(ctx, first.ID, firstSpace.ID)
	if err != nil || len(firstConversations) != 1 || firstConversations[0].Kind != "misty_support" {
		t.Fatalf("first support conversations = %#v, %v", firstConversations, err)
	}
	secondConversations, err := database.SpaceConversations(ctx, second.ID, secondSpace.ID)
	if err != nil || len(secondConversations) != 1 || secondConversations[0].Kind != "misty_support" {
		t.Fatalf("second support conversations = %#v, %v", secondConversations, err)
	}
	if firstConversations[0].ID == secondConversations[0].ID {
		t.Fatal("different accounts received the same support conversation")
	}
	if _, err := database.SpaceConversationMessages(ctx, first.ID, firstSpace.ID, secondConversations[0].ID, 0, 20); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("cross-user support read = %v, want ErrSpaceForbidden", err)
	}
	members, err := database.SpaceMembers(ctx, first.ID, firstSpace.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range members {
		if member.UserID == second.ID {
			t.Fatalf("member directory exposed another customer: %#v", members)
		}
	}
}

func TestOperatorCanManageCanonicalMistySpace(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	operator, _, err := database.GetUserByEmail("test-misty-operator@example.com")
	if err != nil {
		t.Fatal(err)
	}
	space := requireDefaultMistySpace(t, database, ctx, operator.ID)
	for _, permission := range []string{
		PermissionSpaceInvite, PermissionSpaceRename, PermissionSpaceTransfer, PermissionSpaceDelete,
	} {
		if !space.Permissions[permission] {
			t.Fatalf("operator lacks %s: %#v", permission, space.Permissions)
		}
	}
	if _, err := database.RenameSpace(ctx, operator.ID, space.ID, "Misty Support"); err != nil {
		t.Fatalf("RenameSpace(operator canonical Misty) = %v", err)
	}
	if _, err := database.RenameSpace(ctx, operator.ID, space.ID, "Misty"); err != nil {
		t.Fatalf("restore canonical Misty name = %v", err)
	}
}

func requireDefaultMistySpace(
	t *testing.T, database *Database, ctx context.Context, userID string,
) Space {
	t.Helper()
	spaces, err := database.ListSpaces(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	for _, space := range spaces {
		if space.Kind == "misty" {
			return space
		}
	}
	t.Fatalf("no default Misty Space in %#v", spaces)
	return Space{}
}
