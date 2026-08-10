package db

import (
	"context"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestLibraryEditRenditionReservationCompletionAndDownload(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("Rendition Owner", "rendition-owner@example.com", "password123")
	spaceID := createTestSpace(t, database, ctx, owner.ID, "Renditions").ID
	item := createPeopleTestImage(t, database, owner.ID, spaceID, "source.jpg", "7")

	created, err := database.CreateLibraryEditVersion(ctx, owner.ID, spaceID, item.ID, item.Version, LibraryEditDefinition{Rotation: 90, Brightness: 1, Contrast: 1, Saturation: 1})
	if err != nil || created.Edit == nil || created.Edit.RenditionState != "none" {
		t.Fatalf("CreateLibraryEditVersion() = %#v, %v", created, err)
	}
	request, err := database.QueueLibraryEditRendition(ctx, owner.ID, spaceID, item.ID, created.Edit.ID, 512_000)
	if err != nil || request.State != "queued" || request.ReservedBytes != 512_000 {
		t.Fatalf("QueueLibraryEditRendition() = %#v, %v", request, err)
	}
	requestAgain, err := database.QueueLibraryEditRendition(ctx, owner.ID, spaceID, item.ID, created.Edit.ID, 256_000)
	if err != nil || requestAgain.ReservedBytes != request.ReservedBytes {
		t.Fatalf("idempotent QueueLibraryEditRendition() = %#v, %v", requestAgain, err)
	}
	usage, _ := database.SpaceStorageUsage(ctx, owner.ID, spaceID)
	if usage.UsedBytes != 128 || usage.ReservedBytes != 512_000 {
		t.Fatalf("reserved rendition usage = %#v", usage)
	}
	job, err := database.ClaimLibraryRenditionJob(ctx, "rendition-test-worker", time.Minute)
	if err != nil || job == nil || job.EditID != created.Edit.ID || job.ReservedBytes != 512_000 {
		t.Fatalf("ClaimLibraryRenditionJob() = %#v, %v", job, err)
	}
	sha := strings.Repeat("8", 64)
	completed, err := database.CompleteLibraryRenditionJob(ctx, job, "library/renderedobject", "image/jpeg", 256, sha)
	if err != nil || completed.DiscardObjectKey != "" {
		t.Fatalf("CompleteLibraryRenditionJob() = %#v, %v", completed, err)
	}
	usage, _ = database.SpaceStorageUsage(ctx, owner.ID, spaceID)
	if usage.UsedBytes != 384 || usage.ReservedBytes != 0 {
		t.Fatalf("completed rendition usage = %#v", usage)
	}
	versions, err := database.LibraryEditVersions(ctx, owner.ID, spaceID, item.ID)
	if err != nil || len(versions) != 1 || versions[0].RenditionState != "ready" || versions[0].RenditionBytes != 256 || versions[0].RenditionMIME != "image/jpeg" {
		t.Fatalf("LibraryEditVersions() = %#v, %v", versions, err)
	}
	download, err := database.LibraryItemDownload(ctx, owner.ID, spaceID, item.ID)
	if err != nil || !download.Rendition || download.ObjectKey != "library/renderedobject" || download.ByteSize != 256 {
		t.Fatalf("LibraryItemDownload() = %#v, %v", download, err)
	}
	current, err := database.LibraryItem(ctx, owner.ID, spaceID, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := database.SelectLibraryEditVersion(ctx, owner.ID, spaceID, item.ID, "", current.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteLibraryEditVersion(ctx, owner.ID, spaceID, item.ID, created.Edit.ID); err != nil {
		t.Fatal(err)
	}
	usage, _ = database.SpaceStorageUsage(ctx, owner.ID, spaceID)
	if usage.UsedBytes != 384 {
		t.Fatalf("recovery released rendition too early: %#v", usage)
	}
	if _, err := database.Conn.ExecContext(ctx, `UPDATE library_recovery_tombstones SET recover_until=NOW()-INTERVAL '1 second' WHERE target_kind='edit' AND target_id=$1`, created.Edit.ID); err != nil {
		t.Fatal(err)
	}
	purge, err := database.ClaimExpiredLibraryRenditionPurge(ctx, time.Minute)
	if err != nil || purge == nil || purge.ObjectKey != "library/renderedobject" {
		t.Fatalf("ClaimExpiredLibraryRenditionPurge() = %#v, %v", purge, err)
	}
	if err := database.CompleteLibraryRenditionPurge(ctx, purge); err != nil {
		t.Fatal(err)
	}
	usage, _ = database.SpaceStorageUsage(ctx, owner.ID, spaceID)
	if usage.UsedBytes != 128 {
		t.Fatalf("purged rendition usage = %#v", usage)
	}
	download, err = database.LibraryItemDownload(ctx, owner.ID, spaceID, selected.Item.ID)
	if err != nil || download.Rendition || download.ObjectKey == "library/renderedobject" {
		t.Fatalf("post-purge original download = %#v, %v", download, err)
	}
}

func TestExpiredLibraryRenditionReservationReleasesQuota(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("Expired Rendition Owner", "expired-rendition@example.com", "password123")
	spaceID := createTestSpace(t, database, ctx, owner.ID, "Expired renditions").ID
	item := createPeopleTestImage(t, database, owner.ID, spaceID, "source.jpg", "9")
	created, err := database.CreateLibraryEditVersion(ctx, owner.ID, spaceID, item.ID, item.Version, DefaultLibraryEditDefinition())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.QueueLibraryEditRendition(ctx, owner.ID, spaceID, item.ID, created.Edit.ID, 100_000); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn.ExecContext(ctx, `UPDATE space_rendition_reservations SET expires_at=NOW()-INTERVAL '1 second' WHERE source_id=$1`, created.Edit.ID); err != nil {
		t.Fatal(err)
	}
	count, err := database.ReleaseExpiredLibraryRenditionReservations(ctx, 10)
	if err != nil || count != 1 {
		t.Fatalf("ReleaseExpiredLibraryRenditionReservations() = %d, %v", count, err)
	}
	usage, _ := database.SpaceStorageUsage(ctx, owner.ID, spaceID)
	if usage.ReservedBytes != 0 {
		t.Fatalf("expired rendition retained quota: %#v", usage)
	}
	versions, _ := database.LibraryEditVersions(ctx, owner.ID, spaceID, item.ID)
	if len(versions) != 1 || versions[0].RenditionState != "failed" || versions[0].RenditionError != "reservation_expired" {
		t.Fatalf("expired rendition state = %#v", versions)
	}
}
