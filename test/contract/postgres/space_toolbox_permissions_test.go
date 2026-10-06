package db

import (
	"context"
	"slices"
	"testing"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)


import ()

func TestSpaceToolboxDropsWritesWhenPermissionIsRevoked(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Toolbox Permission Owner", "toolbox-permission-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Toolbox Permission Member", "toolbox-permission-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Permission Toolbox")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.TestingInviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := database.SetSpaceMemberPermission(ctx, owner.ID, space.ID, member.ID, PermissionTasksManage, "deny"); err != nil {
		t.Fatal(err)
	}
	names, err := api.TestingResolveAIInvocationSpaceToolNames(ctx, database, member.ID, space.ID, "invocation_permission_denied", "Mark it done")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(names, "tasks.update") || !slices.Contains(names, "tasks.query") {
		t.Fatalf("toolbox must follow the member's current permissions: %v", names)
	}
}
