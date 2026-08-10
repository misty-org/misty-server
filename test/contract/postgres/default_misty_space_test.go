package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestDefaultMistySpaceIsOrdinaryAndPrivatePerAccount(t *testing.T) {
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
	if firstSpace.ID == secondSpace.ID {
		t.Fatalf("default Spaces share identity %q", firstSpace.ID)
	}
	for _, space := range []Space{firstSpace, secondSpace} {
		if space.Kind != "standard" || space.Role != "owner" || space.IsShared {
			t.Fatalf("default Space is not ordinary and private: %#v", space)
		}
		for _, permission := range []string{
			PermissionMessagesRead, PermissionMessagesWrite, PermissionAttachmentUpload,
			PermissionLibraryView, PermissionTasksView, PermissionTasksManage,
			PermissionAgentsRun, PermissionAgentsManage,
		} {
			if !space.Permissions[permission] {
				t.Fatalf("default Space lacks %s: %#v", permission, space.Permissions)
			}
		}
	}
	renamed, err := database.RenameSpace(ctx, first.ID, firstSpace.ID, "Personal home")
	if err != nil {
		t.Fatalf("default Space should support normal lifecycle operations: %v", err)
	}
	if err := database.EnsureDefaultSpace(ctx, first.ID); err != nil {
		t.Fatalf("EnsureDefaultSpace() after rename = %v, want idempotent success", err)
	}
	spacesAfterRename, err := database.ListSpaces(ctx, first.ID)
	if err != nil || len(spacesAfterRename) != 1 || spacesAfterRename[0].ID != renamed.ID {
		t.Fatalf("Spaces after rename and ensure = %#v, %v, want the same single Space", spacesAfterRename, err)
	}
	if spacesAfterRename[0].Permissions[PermissionSpaceDelete] {
		t.Fatalf("ordinary account can delete default Misty Space: %#v", spacesAfterRename[0].Permissions)
	}
	if err := database.DeleteSpace(ctx, first.ID, renamed.ID, renamed.Name); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("DeleteSpace(default Misty) = %v, want ErrSpaceForbidden", err)
	}
}

func TestOperatorCanDeleteDefaultMistySpace(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	operator, err := database.CreateUser("Misty Operator", "misty-operator@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO misty_space_operators(user_id) VALUES($1)`, operator.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	space := requireDefaultMistySpace(t, database, ctx, operator.ID)
	if !space.Permissions[PermissionSpaceDelete] {
		t.Fatalf("operator lacks default Misty deletion permission: %#v", space.Permissions)
	}
	if err := database.DeleteSpace(ctx, operator.ID, space.ID, space.Name); err != nil {
		t.Fatalf("DeleteSpace(operator default Misty) = %v", err)
	}
	if err := database.EnsureDefaultSpace(ctx, operator.ID); err != nil {
		t.Fatalf("EnsureDefaultSpace() after operator deletion = %v, want idempotent success", err)
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
		if space.Name == "Misty" && space.OwnerUserID == userID {
			return space
		}
	}
	t.Fatalf("no default Misty Space in %#v", spaces)
	return Space{}
}
