package db

import (
	"context"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestManagedMistyIsSingleFixedAndSupportsBoundedHiddenWorkers(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Managed Misty Owner", "managed-misty-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Managed Misty Work")
	if err != nil {
		t.Fatal(err)
	}
	misty, err := database.EnsureManagedMistyAgent(ctx, owner.ID, "google/gemini-2.5-flash-lite")
	if err != nil {
		t.Fatal(err)
	}
	replayedIdentity, err := database.EnsureManagedMistyAgent(ctx, owner.ID, "google/gemini-2.5-flash-lite")
	if err != nil {
		t.Fatal(err)
	}
	if !misty.SystemManaged || misty.ID != replayedIdentity.ID || misty.Name != "Misty" || misty.ModelMode != "pinned" || misty.DefaultRunMode != "auto" {
		t.Fatalf("managed identity = %#v, replay = %#v", misty, replayedIdentity)
	}

	parent, err := database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, misty.ID, CreatorAgentRunInput{
		Instruction: "Coordinate this work", AIIdempotencyKey: "managed-misty-run-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	replayedRun, err := database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, misty.ID, CreatorAgentRunInput{
		Instruction: "Coordinate this work", AIIdempotencyKey: "managed-misty-run-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayedRun.ID != parent.ID || !replayedRun.IdempotentReplay {
		t.Fatalf("idempotent replay = %#v, parent = %#v", replayedRun, parent)
	}

	for index := 0; index < 3; index++ {
		child, childErr := database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, misty.ID, CreatorAgentRunInput{
			Instruction: "Handle a bounded background subtask", ParentRunID: parent.ID,
		})
		if childErr != nil {
			t.Fatal(childErr)
		}
		if child.ParentRunID != parent.ID || child.DelegationDepth != 1 || child.AgentID != misty.ID {
			t.Fatalf("hidden child = %#v", child)
		}
	}
	if _, err := database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, misty.ID, CreatorAgentRunInput{
		Instruction: "Exceed worker fanout", ParentRunID: parent.ID,
	}); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("fourth hidden worker = %v, want conflict", err)
	}
}
