package db

import (
	"context"
	"testing"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)


import ()

func TestAccountRunSurvivesSpaceRemovalWithoutRetainingSpaceAccess(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Space Owner", "revoke-run-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	creator, err := database.CreateUser("Agent Creator", "revoke-run-creator@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Revocation Space")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.TestingInviteToSpace(ctx, owner.ID, space.ID, creator.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, creator.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	agent, err := database.EnsureAskIdentity(ctx, creator.ID, "google/gemini-2.5-flash-lite")
	if err != nil {
		t.Fatal(err)
	}
	run, err := database.CreateCreatorAgentRun(ctx, creator.ID, space.ID, agent.ID, CreatorAgentRunInput{Instruction: "Work here"})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.RemoveSpaceMember(ctx, owner.ID, space.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	state, _, err := database.PersonalAgentTaskRunJobState(ctx, run.ID)
	if err != nil || state != "queued" {
		t.Fatalf("job state after membership revocation = %q, %v", state, err)
	}
	if _, err := database.SpaceByID(ctx, creator.ID, space.ID); err == nil {
		t.Fatal("removed member retained Space data access")
	}
}
