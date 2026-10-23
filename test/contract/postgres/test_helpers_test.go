package db

import (
	"context"
)

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/test/testkit"
)

func openTestDatabase(t *testing.T) *Database {
	t.Helper()
	return testkit.OpenDatabase(t)
}

func createTestSpace(t *testing.T, database *Database, ctx context.Context, ownerUserID, name string) *Space {
	t.Helper()
	space, err := database.TestingCreateSpace(ctx, ownerUserID, name)
	if err != nil {
		t.Fatalf("CreateSpace(%q) error = %v", name, err)
	}
	return space
}

// standardSpaces leaves out the personal Space every account starts with.
func standardSpaces(spaces []Space) []Space {
	out := []Space{}
	for _, space := range spaces {
		if !space.IsDefault {
			out = append(out, space)
		}
	}
	return out
}

// personalSpace is the protected Space an account gets at sign-up.
func personalSpace(t *testing.T, database *Database, ctx context.Context, userID string) Space {
	t.Helper()
	spaces, err := database.ListSpaces(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	for _, space := range spaces {
		if space.IsDefault && space.OwnerUserID == userID {
			return space
		}
	}
	t.Fatalf("account %s has no personal Space: %#v", userID, spaces)
	return Space{}
}
