package db

import (
	"context"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestFirstOwnedSpaceBecomesTheProtectedDefault(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Default owner", "default-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}

	spaces, err := database.ListSpaces(ctx, owner.ID)
	if err != nil || len(spaces) != 0 {
		t.Fatalf("new account Spaces = %#v, %v, want none before onboarding", spaces, err)
	}

	first := createTestSpace(t, database, ctx, owner.ID, "Home")
	if !first.IsDefault || first.Role != "owner" {
		t.Fatalf("first Space = %#v, want owned default", first)
	}
	first, err = database.SpaceByID(ctx, owner.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, permission := range []string{
		PermissionSpaceInvite, PermissionSpaceRename, PermissionStorageViewOwn,
	} {
		if !first.Permissions[permission] {
			t.Fatalf("default Space lacks %s: %#v", permission, first.Permissions)
		}
	}
	for _, permission := range []string{PermissionSpaceTransfer, PermissionSpaceDelete} {
		if first.Permissions[permission] {
			t.Fatalf("default Space unexpectedly grants %s: %#v", permission, first.Permissions)
		}
	}

	renamed, err := database.RenameSpace(ctx, owner.ID, first.ID, "Personal")
	if err != nil || renamed.Name != "Personal" {
		t.Fatalf("RenameSpace(default) = %#v, %v", renamed, err)
	}
	if err := database.DeleteSpace(ctx, owner.ID, first.ID, renamed.Name); !errors.Is(err, ErrDefaultSpaceProtected) {
		t.Fatalf("DeleteSpace(default) = %v, want ErrDefaultSpaceProtected", err)
	}

	second := createTestSpace(t, database, ctx, owner.ID, "Project")
	if second.IsDefault {
		t.Fatalf("second Space unexpectedly became default: %#v", second)
	}
	if err := database.DeleteSpace(ctx, owner.ID, second.ID, second.Name); err != nil {
		t.Fatalf("DeleteSpace(non-default) = %v", err)
	}
}

func TestDefaultSpaceCannotBeTransferred(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Transfer owner", "default-transfer-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Transfer member", "default-transfer-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Home")
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := database.TransferSpaceOwnership(ctx, owner.ID, space.ID, member.ID); !errors.Is(err, ErrDefaultSpaceProtected) {
		t.Fatalf("TransferSpaceOwnership(default) = %v, want ErrDefaultSpaceProtected", err)
	}
}
