package db

import (
	"context"
	"errors"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestLibraryPreviewDerivativeAuthorizationAndReuse(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("Preview Owner", "preview-owner@example.com", "password123")
	outsider, _ := database.CreateUser("Preview Outsider", "preview-outsider@example.com", "password123")
	spaceID := createTestSpace(t, database, ctx, owner.ID, "Previews").ID
	item := createPeopleTestImage(t, database, owner.ID, spaceID, "preview.jpg", "1")
	baseline, err := database.OwnerStorageUsage(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	source, err := database.LibraryItemPreviewSource(ctx, owner.ID, spaceID, item.ID, false)
	if err != nil || source.SourceIdentity == "" || source.PreviewObjectKey != "" {
		t.Fatalf("preview source = %#v, %v", source, err)
	}
	if _, err := database.LibraryItemPreviewSource(ctx, outsider.ID, spaceID, item.ID, false); err == nil {
		t.Fatal("outsider received a preview source")
	}
	if _, err := database.LibraryItemPreviewSource(ctx, owner.ID, spaceID, item.ID, false); !errors.Is(err, ErrLibraryConflict) {
		t.Fatalf("concurrent preview source error = %v, want ErrLibraryConflict", err)
	}
	reserved, err := database.OwnerStorageUsage(ctx, owner.ID)
	if err != nil || reserved.ReservedBytes != baseline.ReservedBytes+25_000_000 {
		t.Fatalf("preview reservation usage = %#v, %v", reserved, err)
	}
	if err := database.ReleaseLibraryPreviewReservation(ctx, owner.ID, spaceID, item.ID); err != nil {
		t.Fatal(err)
	}
	released, err := database.OwnerStorageUsage(ctx, owner.ID)
	if err != nil || released.ReservedBytes != baseline.ReservedBytes {
		t.Fatalf("released preview reservation usage = %#v, %v", released, err)
	}
	source, err = database.LibraryItemPreviewSource(ctx, owner.ID, spaceID, item.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 64)
	completed, err := database.CompleteLibraryPreview(ctx, owner.ID, spaceID, item.ID, source.SourceIdentity, "library/preview-object-123456", "image/jpeg", 64_000, sha, false)
	if err != nil || completed.ObjectKey != "library/preview-object-123456" {
		t.Fatalf("complete preview = %#v, %v", completed, err)
	}
	settled, err := database.OwnerStorageUsage(ctx, owner.ID)
	if err != nil || settled.ReservedBytes != baseline.ReservedBytes || settled.UsedBytes != baseline.UsedBytes+64_000 {
		t.Fatalf("settled preview usage = %#v, %v", settled, err)
	}
	source, err = database.LibraryItemPreviewSource(ctx, owner.ID, spaceID, item.ID, false)
	if err != nil || source.PreviewObjectKey != completed.ObjectKey || source.PreviewBytes != 64_000 || source.PreviewSHA256 != sha {
		t.Fatalf("reused preview source = %#v, %v", source, err)
	}
	repairedKey := "library/preview-object-repaired"
	selectedKey, err := database.ReplaceMissingLibraryPreviewDeduplicationObject(ctx, owner.ID, spaceID, item.ID, source.SourceIdentity, completed.ObjectKey, repairedKey)
	if err != nil || selectedKey != repairedKey {
		t.Fatalf("replace missing preview = %q, %v", selectedKey, err)
	}
	source, err = database.LibraryItemPreviewSource(ctx, owner.ID, spaceID, item.ID, false)
	if err != nil || source.PreviewObjectKey != repairedKey || source.PreviewBytes != 64_000 || source.PreviewSHA256 != sha {
		t.Fatalf("repaired preview source = %#v, %v", source, err)
	}
	selectedKey, err = database.ReplaceMissingLibraryPreviewDeduplicationObject(ctx, owner.ID, spaceID, item.ID, source.SourceIdentity, completed.ObjectKey, "library/concurrent-preview-object")
	if err != nil || selectedKey != repairedKey {
		t.Fatalf("concurrent preview repair = %q, %v", selectedKey, err)
	}
}
