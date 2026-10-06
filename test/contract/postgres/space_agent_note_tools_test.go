package db

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)


import ()

func TestSpaceAgentCreatesReadsAndUpdatesNativeNote(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Note Tool Owner", "note-tool-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Notes Space")
	if err != nil {
		t.Fatal(err)
	}
	createdRaw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Create a launch note", "notes.create", json.RawMessage(`{"title":"Launch plan","markdown":"# Launch\nInitial checklist"}`))
	if err != nil {
		t.Fatal(err)
	}
	var created SpaceNote
	if err := json.Unmarshal(createdRaw, &created); err != nil || created.ID == "" {
		t.Fatalf("created note = %s, %v", createdRaw, err)
	}
	updatedRaw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Update the launch note", "notes.update", json.RawMessage(`{"id":"`+created.ID+`","title":"Launch plan","markdown":"# Launch\nReady for beta"}`))
	if err != nil {
		t.Fatal(err)
	}
	var updated SpaceNote
	if json.Unmarshal(updatedRaw, &updated) != nil || updated.ID != created.ID {
		t.Fatalf("updated note = %s", updatedRaw)
	}
	commands, err := database.PendingNoteControlCommands(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	foundReplace := false
	for _, command := range commands {
		if command.NoteID == created.ID && command.Command == "replace_markdown" {
			foundReplace = true
		}
	}
	if !foundReplace {
		t.Fatalf("missing durable replace_markdown command: %#v", commands)
	}
	applied, err := database.ApplySpaceNoteProjection(ctx, SpaceNoteProjection{
		NoteID: created.ID, Revision: created.CollaborationRevision + 1, Title: "Launch plan",
		Markdown: "# Launch\nReady for beta", PlainText: "Launch\nReady for beta",
	})
	if err != nil || !applied {
		t.Fatalf("room projection was not applied: applied=%v err=%v", applied, err)
	}
	searchRaw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Find launch notes", "notes.search", json.RawMessage(`{"query":"Ready for beta"}`))
	if err != nil || !strings.Contains(string(searchRaw), created.ID) {
		t.Fatalf("searched notes = %s, %v", searchRaw, err)
	}

	readRaw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Read the launch note", "notes.read", json.RawMessage(`{"id":"`+created.ID+`"}`))
	var read struct {
		Markdown *string `json:"markdown"`
		Empty    bool    `json:"empty"`
	}
	if err != nil || json.Unmarshal(readRaw, &read) != nil || read.Markdown == nil || *read.Markdown != "# Launch\nReady for beta" || read.Empty {
		t.Fatalf("read note = %s, %v", readRaw, err)
	}
	// Adding a line keeps the body instead of asking the model to rewrite it.
	if _, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Add a line to the launch note", "notes.update", json.RawMessage(`{"id":"`+created.ID+`","append":"Book the venue."}`)); err != nil {
		t.Fatal(err)
	}
	commands, err = database.PendingNoteControlCommands(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	appended := false
	for _, command := range commands {
		appended = appended || (command.NoteID == created.ID && strings.Contains(string(command.Payload), `# Launch\nReady for beta\n\nBook the venue.`))
	}
	if !appended {
		t.Fatalf("append did not keep the body: %#v", commands)
	}
	if _, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Update the launch note", "notes.update", json.RawMessage(`{"id":"`+created.ID+`","markdown":"x","append":"y"}`)); err == nil {
		t.Fatal("accepted both a replacement body and an append")
	}
}

func TestSpaceAgentReadsAnEmptyNoteAsEmpty(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Empty Note Owner", "empty-note-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Empty Notes")
	if err != nil {
		t.Fatal(err)
	}
	note, err := database.CreateSpaceNote(ctx, owner.ID, space.ID, "Launch checklist")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Read the checklist", "notes.read", json.RawMessage(`{"id":"`+note.ID+`"}`))
	if err != nil || !strings.Contains(string(raw), `"markdown":""`) || !strings.Contains(string(raw), `"empty":true`) {
		t.Fatalf("empty note read = %s, %v", raw, err)
	}
}
