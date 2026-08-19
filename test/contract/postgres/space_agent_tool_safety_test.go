package db

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestSpaceAgentWriteCallbacksAreIdempotent(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser(
		"Idempotent Tool Owner",
		"idempotent-tool-owner@example.com",
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Idempotent Tool Space")
	if err != nil {
		t.Fatal(err)
	}
	arguments := json.RawMessage(`{
		"title":"Investor demo",
		"markdown":"# Demo\nVerify the complete Agent toolbox"
	}`)

	firstRaw, err := api.TestingExecuteSpaceConversationTool(
		ctx,
		database,
		owner.ID,
		space.ID,
		"",
		"Create an investor demo note",
		"notes.create",
		arguments,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondRaw, err := api.TestingExecuteSpaceConversationTool(
		ctx,
		database,
		owner.ID,
		space.ID,
		"",
		"Create an investor demo note",
		"notes.create",
		arguments,
	)
	if err != nil {
		t.Fatal(err)
	}
	var first, second SpaceNote
	if json.Unmarshal(firstRaw, &first) != nil || json.Unmarshal(secondRaw, &second) != nil {
		t.Fatalf("note results are invalid: %s, %s", firstRaw, secondRaw)
	}
	if first.ID == "" || second.ID != first.ID {
		t.Fatalf("duplicate callback created different notes: %q, %q", first.ID, second.ID)
	}
	notes, err := database.AccessibleSpaceNotes(ctx, owner.ID, space.ID)
	if err != nil || len(notes) != 1 {
		t.Fatalf("persisted notes = %#v, %v", notes, err)
	}
}

func TestSpaceAgentCannotReadContentFromAnotherSpace(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser(
		"Isolation Tool Owner",
		"isolation-tool-owner@example.com",
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	source, err := database.CreateSpace(ctx, owner.ID, "Private Source Space")
	if err != nil {
		t.Fatal(err)
	}
	target, err := database.CreateSpace(ctx, owner.ID, "Target Agent Space")
	if err != nil {
		t.Fatal(err)
	}
	note, err := database.CreateSpaceNoteWithAudience(
		ctx,
		owner.ID,
		source.ID,
		"Private investor notes",
		SpaceResourceAudience{Kind: SpaceAudienceSpace},
		"Do not expose this to another Space.",
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = api.TestingExecuteSpaceConversationTool(
		ctx,
		database,
		owner.ID,
		target.ID,
		"",
		"Read the note",
		"notes.read",
		json.RawMessage(`{"id":"`+note.ID+`"}`),
	)
	if !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("cross-Space note read error = %v, want Space not found", err)
	}
}
