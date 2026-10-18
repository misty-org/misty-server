package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestCodeProjectsAreLinkedToSpacesByTheirOwner(t *testing.T) {
	f := newMemberRequestFixture(t)
	ctx := context.Background()
	link := SpaceCodeProjectInput{ProjectID: "scope_site", DeviceID: "device_mac", Name: "Website", GitHubRepo: "bob/site"}
	project, err := f.database.LinkSpaceCodeProject(ctx, f.bob.ID, f.space.ID, link)
	if err != nil || project.OwnerName != f.bob.Username || project.GitHubRepo != "bob/site" {
		t.Fatalf("linked project = %#v, %v", project, err)
	}
	link.Name = "Marketing site"
	again, err := f.database.LinkSpaceCodeProject(ctx, f.bob.ID, f.space.ID, link)
	if err != nil || again.ID != project.ID || again.Name != "Marketing site" {
		t.Fatalf("linking the same project again = %#v, %v, want the same link renamed", again, err)
	}
	if _, err := f.database.LinkSpaceCodeProject(ctx, f.bob.ID, f.space.ID, SpaceCodeProjectInput{ProjectID: "x", Name: "Bad", GitHubRepo: "not a repo"}); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("an invalid repository = %v", err)
	}
	projects, err := f.database.SpaceCodeProjects(ctx, f.alice.ID, f.space.ID)
	if err != nil || len(projects) != 1 || projects[0].ID != project.ID {
		t.Fatalf("Alice's view = %#v, %v", projects, err)
	}

	// Another member can't unlink it; the Space's owner can.
	carol, err := f.database.CreateUser("carol-code-projects@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	addTestSpaceMember(t, f.database, f.space.ID, carol.ID)
	if err := f.database.UnlinkSpaceCodeProject(ctx, carol.ID, f.space.ID, project.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("another member unlinking = %v", err)
	}
	if err := f.database.UnlinkSpaceCodeProject(ctx, f.alice.ID, f.space.ID, project.ID); err != nil {
		t.Fatal(err)
	}

	// A person's projects leave with them.
	if _, err := f.database.LinkSpaceCodeProject(ctx, f.bob.ID, f.space.ID, link); err != nil {
		t.Fatal(err)
	}
	if err := f.database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM space_members WHERE space_id=$1 AND user_id=$2`, f.space.ID, f.bob.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if projects, err := f.database.SpaceCodeProjects(ctx, f.alice.ID, f.space.ID); err != nil || len(projects) != 0 {
		t.Fatalf("a former member's projects are still linked: %#v, %v", projects, err)
	}
}
