package db

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestSpaceAgentReadsUpdatesAndPromotesLibraryItems(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Library Tool Owner", "library-tool-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Library Tool Space")
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	upload, err := database.CreateLibraryUpload(ctx, owner.ID, space.ID, "library", "brief.txt", "text/plain", 10, digest, "library/agent-tool-brief", "library-agent-token", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetLibraryUploadState(ctx, owner.ID, space.ID, upload.ID, "library-agent-token", "initiated", "uploaded_unverified"); err != nil {
		t.Fatal(err)
	}
	completed, err := database.CompleteLibraryUpload(ctx, owner.ID, space.ID, upload.ID, "library-agent-token", 10, digest, "text/plain", nil)
	if err != nil || completed.Item == nil {
		t.Fatalf("completed Library upload = %#v, %v", completed, err)
	}
	readRaw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Read the library item", "library.read", json.RawMessage(`{"id":"`+completed.Item.ID+`"}`))
	if err != nil || !strings.Contains(string(readRaw), "brief.txt") {
		t.Fatalf("read Library item = %s, %v", readRaw, err)
	}
	updatedRaw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Tag the file in the library", "library.update", json.RawMessage(`{"id":"`+completed.Item.ID+`","displayName":"Investor brief","tags":["demo","investor"],"favorite":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var updated SpaceLibraryItem
	if json.Unmarshal(updatedRaw, &updated) != nil || updated.DisplayName != "Investor brief" || !updated.Favorite || len(updated.Tags) != 2 {
		t.Fatalf("updated Library item = %s", updatedRaw)
	}
	attachmentUpload, err := database.CreateLibraryUpload(ctx, owner.ID, space.ID, "attachment", "evidence.txt", "text/plain", 10, digest, "library/agent-tool-attachment", "attachment-agent-token", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetLibraryUploadState(ctx, owner.ID, space.ID, attachmentUpload.ID, "attachment-agent-token", "initiated", "uploaded_unverified"); err != nil {
		t.Fatal(err)
	}
	attachmentResult, err := database.CompleteLibraryUpload(ctx, owner.ID, space.ID, attachmentUpload.ID, "attachment-agent-token", 10, digest, "text/plain", nil)
	if err != nil || attachmentResult.Attachment == nil {
		t.Fatalf("completed attachment upload = %#v, %v", attachmentResult, err)
	}
	promotedRaw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Save the attachment to the library", "library.promote_attachment", json.RawMessage(`{"attachmentId":"`+attachmentResult.Attachment.ID+`"}`))
	if err != nil || !strings.Contains(string(promotedRaw), attachmentResult.Attachment.DisplayName) {
		t.Fatalf("promoted attachment = %s, %v", promotedRaw, err)
	}
}
