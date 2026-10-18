package db

import (
	"context"
	"errors"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"testing"
)

import ()

func TestEveryAccountStartsWithAProtectedPersonalSpace(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("default-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}

	spaces, err := database.ListSpaces(ctx, owner.ID)
	if err != nil || len(spaces) != 1 || !spaces[0].IsDefault || spaces[0].Role != "owner" || spaces[0].Name != owner.Username+"'s Space" {
		t.Fatalf("new account Spaces = %#v, %v, want one personal Space named after them", spaces, err)
	}

	first := &spaces[0]
	first, err = database.SpaceByID(ctx, owner.ID, first.ID)
	if err != nil || first.Role != "owner" || !first.IsDefault {
		t.Fatalf("SpaceByID(default) = %#v, %v", first, err)
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
	owner, err := database.CreateUser("default-transfer-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("default-transfer-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := personalSpace(t, database, ctx, owner.ID)
	invite, err := database.TestingInviteToSpace(ctx, owner.ID, space.ID, member.Email)
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
