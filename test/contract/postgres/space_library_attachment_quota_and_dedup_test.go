package db

import (
	"context"
	. "github.com/misty-org/misty-server/internal/platform/db"
	"strings"
	"testing"
	"time"
)

func TestAttachmentQuotaUploadAndDedup(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("library-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Library")
	spaceID := space.ID
	digest := strings.Repeat("a", 64)

	upload, err := database.CreateLibraryUpload(ctx, owner.ID, spaceID, "attachment", "first.txt", "text/plain", 100, digest, "library/firstobject", "token-1", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("CreateLibraryUpload() error = %v", err)
	}
	usage, _ := database.SpaceStorageUsage(ctx, owner.ID, spaceID)
	if usage.ReservedBytes != 100 || usage.UsedBytes != 0 {
		t.Fatalf("reserved usage = %#v", usage)
	}
	if _, err := database.SetLibraryUploadState(ctx, owner.ID, spaceID, upload.ID, "token-1", "initiated", "uploaded_unverified"); err != nil {
		t.Fatal(err)
	}
	completed, err := database.CompleteLibraryUpload(ctx, owner.ID, spaceID, upload.ID, "token-1", 100, digest, "text/plain; charset=utf-8", nil)
	if err != nil || completed.Attachment == nil {
		t.Fatalf("CompleteLibraryUpload() = %#v, %v", completed, err)
	}
	usage, _ = database.SpaceStorageUsage(ctx, owner.ID, spaceID)
	if usage.ReservedBytes != 0 || usage.UsedBytes != 100 {
		t.Fatalf("finalized usage = %#v", usage)
	}
	if _, err := database.Conn.ExecContext(ctx, `UPDATE space_storage_usage SET used_bytes=0 WHERE space_id=$1`, spaceID); err != nil {
		t.Fatal(err)
	}
	usage, _ = database.SpaceStorageUsage(ctx, owner.ID, spaceID)
	if usage.SpaceUsedBytes != 100 || usage.UsedBytes != 100 {
		t.Fatalf("usage read did not repair stale per-Space cache = %#v", usage)
	}

	attachmentUpload, err := database.CreateLibraryUpload(ctx, owner.ID, spaceID, "attachment", "second.txt", "text/plain", 100, digest, "library/secondobject", "token-2", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetLibraryUploadState(ctx, owner.ID, spaceID, attachmentUpload.ID, "token-2", "initiated", "uploaded_unverified"); err != nil {
		t.Fatal(err)
	}
	attachmentResult, err := database.CompleteLibraryUpload(ctx, owner.ID, spaceID, attachmentUpload.ID, "token-2", 100, digest, "text/plain; charset=utf-8", nil)
	if err != nil || attachmentResult.Attachment == nil || attachmentResult.DiscardObjectKey != "library/secondobject" {
		t.Fatalf("deduplicated attachment = %#v, %v", attachmentResult, err)
	}
	usage, _ = database.SpaceStorageUsage(ctx, owner.ID, spaceID)
	if usage.UsedBytes != 200 {
		t.Fatalf("two independent contributions = %#v", usage)
	}
	// The Library item purpose is gone.
	if _, err := database.CreateLibraryUpload(ctx, owner.ID, spaceID, "library", "third.txt", "text/plain", 100, digest, "library/thirdobject", "token-3", time.Now().Add(time.Hour)); err == nil {
		t.Fatal("a Library item upload was started")
	}
	message, _, err := database.TestingCreateSpaceMessageWithReferences(ctx, owner.ID, spaceID, nil, nil, []string{attachmentResult.Attachment.ID}, "")
	if err != nil {
		t.Fatalf("CreateSpaceMessageWithReferences(attachment-only) error = %v", err)
	}
	reply, _, err := database.TestingCreateSpaceMessageWithReferences(ctx, owner.ID, spaceID, []MessageSpan{{Type: "text", Text: "reply"}}, nil, nil, message.ID)
	if err != nil || reply.ReplyToMessageID != message.ID {
		t.Fatalf("reply = %#v, %v", reply, err)
	}
	messages, err := database.SpaceMessages(ctx, owner.ID, spaceID, 0, 20)
	if err != nil || len(messages) != 2 || len(messages[1].Attachments) != 1 {
		t.Fatalf("SpaceMessages() = %#v, %v", messages, err)
	}
}
