package db

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMissingDeduplicationObjectCanBeReplacedByNewUpload(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("library-repair-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	spaceID := createTestSpace(t, database, ctx, owner.ID, "Library repair").ID
	digest := strings.Repeat("b", 64)

	first, err := database.CreateLibraryUpload(ctx, owner.ID, spaceID, "attachment", "first.jpg", "image/jpeg", 128, digest, "library/originalobject", "first-token", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetLibraryUploadState(ctx, owner.ID, spaceID, first.ID, "first-token", "initiated", "uploaded_unverified"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CompleteLibraryUpload(ctx, owner.ID, spaceID, first.ID, "first-token", 128, digest, "image/jpeg", nil); err != nil {
		t.Fatal(err)
	}

	replacement, err := database.CreateLibraryUpload(ctx, owner.ID, spaceID, "attachment", "replacement.jpg", "image/jpeg", 128, digest, "library/replacementobject", "replacement-token", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetLibraryUploadState(ctx, owner.ID, spaceID, replacement.ID, "replacement-token", "initiated", "uploaded_unverified"); err != nil {
		t.Fatal(err)
	}
	candidate, err := database.LibraryUploadDeduplicationObjectKey(ctx, owner.ID, spaceID, replacement.ID)
	if err != nil || candidate != "library/originalobject" {
		t.Fatalf("LibraryUploadDeduplicationObjectKey() = %q, %v", candidate, err)
	}
	if err := database.ReplaceMissingLibraryUploadDeduplicationObject(ctx, owner.ID, spaceID, replacement.ID, candidate); err != nil {
		t.Fatalf("ReplaceMissingLibraryUploadDeduplicationObject() error = %v", err)
	}
	completed, err := database.CompleteLibraryUpload(ctx, owner.ID, spaceID, replacement.ID, "replacement-token", 128, digest, "image/jpeg", nil)
	if err != nil || completed.Attachment == nil || completed.DiscardObjectKey != "" {
		t.Fatalf("CompleteLibraryUpload() = %#v, %v", completed, err)
	}
	download, err := database.MessageAttachmentDownload(ctx, owner.ID, spaceID, completed.Attachment.ID)
	if err != nil || download.ObjectKey != "library/replacementobject" {
		t.Fatalf("MessageAttachmentDownload() = %#v, %v", download, err)
	}
}
