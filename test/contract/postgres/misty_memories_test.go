package db

import (
	"context"
	"errors"
	"testing"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)


import ()

func TestMistyMemoryIsExplicitPrivateScopedAndControllable(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("memory-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("memory-other@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	family, err := database.TestingCreateSpace(ctx, owner.ID, "Memory Family")
	if err != nil {
		t.Fatal(err)
	}
	work, err := database.TestingCreateSpace(ctx, owner.ID, "Memory Work")
	if err != nil {
		t.Fatal(err)
	}
	personal, err := database.RememberMistyMemory(ctx, owner.ID, RememberMistyMemoryInput{
		Kind: "preference", Content: "Use concise answers by default", Reason: "The user explicitly asked",
	})
	if err != nil {
		t.Fatal(err)
	}
	spaceMemory, err := database.RememberMistyMemory(ctx, owner.ID, RememberMistyMemoryInput{
		SpaceID: family.ID, Kind: "instruction", Content: "Call the shared list the family board",
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := database.RememberMistyMemory(ctx, owner.ID, RememberMistyMemoryInput{
		SpaceID: family.ID, Kind: "instruction", Content: "  Call   the shared list the family board  ",
	})
	if err != nil || duplicate.ID != spaceMemory.ID {
		t.Fatalf("duplicate memory = %#v, %v", duplicate, err)
	}

	// Each Space has its own memory tree; the personal Space is one of them.
	familyContext, err := database.MistyMemoryContext(ctx, owner.ID, family.ID, 20)
	if err != nil || hasMistyMemory(familyContext, personal.ID) || !hasMistyMemory(familyContext, spaceMemory.ID) {
		t.Fatalf("family context = %#v, %v", familyContext, err)
	}
	workContext, err := database.MistyMemoryContext(ctx, owner.ID, work.ID, 20)
	if err != nil || len(workContext) != 0 {
		t.Fatalf("memory crossed into another Space = %#v, %v", workContext, err)
	}
	personalContext, err := database.MistyMemoryContext(ctx, owner.ID, "", 20)
	if err != nil || !hasMistyMemory(personalContext, personal.ID) || hasMistyMemory(personalContext, spaceMemory.ID) {
		t.Fatalf("personal context = %#v, %v", personalContext, err)
	}
	otherContext, err := database.MistyMemoryContext(ctx, other.ID, "", 20)
	if err != nil || len(otherContext) != 0 {
		t.Fatalf("memory crossed account boundary = %#v, %v", otherContext, err)
	}

	settings, err := database.UpdateAISettings(ctx, owner.ID, true, 30, true, false)
	if err != nil || settings.MemoryEnabled {
		t.Fatalf("memory disable = %#v, %v", settings, err)
	}
	disabledContext, err := database.MistyMemoryContext(ctx, owner.ID, family.ID, 20)
	if err != nil || len(disabledContext) != 0 {
		t.Fatalf("disabled memory remained in prompt context = %#v, %v", disabledContext, err)
	}
	if _, err := database.RememberMistyMemory(ctx, owner.ID, RememberMistyMemoryInput{Content: "Do not store this"}); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("disabled memory accepted a write: %v", err)
	}
	if _, err := database.UpdateAISettings(ctx, owner.ID, true, 30, true, true); err != nil {
		t.Fatal(err)
	}
	if err := database.ForgetMistyMemory(ctx, owner.ID, "", personal.ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := database.MistyMemories(ctx, owner.ID, "", 100)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("forget/list result = %#v, %v", remaining, err)
	}
	if kept, err := database.MistyMemories(ctx, owner.ID, family.ID, 100); err != nil || !hasMistyMemory(kept, spaceMemory.ID) {
		t.Fatalf("forgetting in Personal touched the family Space = %#v, %v", kept, err)
	}
}

func hasMistyMemory(items []MistyMemory, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
