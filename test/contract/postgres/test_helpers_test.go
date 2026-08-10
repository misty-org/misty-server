package db

import (
	"context"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/test/testkit"
)

func openTestDatabase(t *testing.T) *Database {
	t.Helper()
	return testkit.OpenDatabase(t)
}

func createTestSpace(t *testing.T, database *Database, ctx context.Context, ownerUserID, name string) *Space {
	t.Helper()
	space, err := database.CreateSpace(ctx, ownerUserID, name)
	if err != nil {
		t.Fatalf("CreateSpace(%q) error = %v", name, err)
	}
	return space
}

func standardSpaces(spaces []Space) []Space {
	out := []Space{}
	for _, space := range spaces {
		if space.Kind != "misty" {
			out = append(out, space)
		}
	}
	return out
}
