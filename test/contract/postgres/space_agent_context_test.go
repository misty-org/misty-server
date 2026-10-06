package db

import (
	"context"
	"testing"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)


import ()


// The revision token decides whether the send path rebuilds context. It has to
// move when content the agent can see changes, and stay put otherwise, or every
// turn either serves stale context or re-pays the full prompt cost.
func TestSpaceContextRevisionTracksVisibleChanges(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Revision Owner", "revision-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Revision Member", "revision-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Revision Space")
	if err != nil {
		t.Fatal(err)
	}

	first, err := database.SpaceContextRevision(ctx, owner.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" {
		t.Fatal("SpaceContextRevision() returned an empty token")
	}
	again, err := database.SpaceContextRevision(ctx, owner.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Fatal("revision changed with no Space activity; context would be rebuilt every turn")
	}

	if _, err := database.CreateSpaceTask(ctx, owner.ID, SpaceTask{
		SpaceID: space.ID, Title: "Added after the first turn", Status: "todo",
	}); err != nil {
		t.Fatal(err)
	}
	afterTask, err := database.SpaceContextRevision(ctx, owner.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterTask == first {
		t.Fatal("revision did not move after a task was added; the agent would answer from stale context")
	}

	invite, err := database.TestingInviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	afterMember, err := database.SpaceContextRevision(ctx, owner.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterMember == afterTask {
		t.Fatal("revision did not move after a member joined")
	}

	// Tokens remain per member, so one account's cached context is never reused
	// for another account even though the Member defaults are fixed.
	memberToken, err := database.SpaceContextRevision(ctx, member.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	ownerToken, err := database.SpaceContextRevision(ctx, owner.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ownerToken == memberToken {
		t.Fatal("two members produced the same revision; caller identity would be ignored")
	}
}
