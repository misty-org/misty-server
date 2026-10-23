package db

import (
	"context"
	"encoding/json"
	"errors"
	. "github.com/kannachi323/misty/server/internal/platform/db"
	"strings"
	"testing"
	"time"
)

func TestLibraryQuotaUploadDedupAndAttachmentPromotion(t *testing.T) {
	database := openTestDatabase(t)
	useResourceAdapterFixture(t, database)
	ctx := context.Background()
	owner, err := database.CreateUser("library-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Library")
	spaceID := space.ID
	digest := strings.Repeat("a", 64)

	upload, err := database.CreateLibraryUpload(ctx, owner.ID, spaceID, "library", "first.txt", "text/plain", 100, digest, "library/firstobject", "token-1", time.Now().Add(time.Hour))
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
	if err != nil || completed.Item == nil {
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
	promoted, err := database.PromoteMessageAttachment(ctx, owner.ID, spaceID, attachmentResult.Attachment.ID)
	if err != nil {
		t.Fatalf("PromoteMessageAttachment() error = %v", err)
	}
	assertLibraryRealtimeEvent(t, database, ctx, owner.ID, "library.item.promoted", promoted.ID)
	usage, _ = database.SpaceStorageUsage(ctx, owner.ID, spaceID)
	if usage.UsedBytes != 200 {
		t.Fatalf("promotion double-charged quota: %#v", usage)
	}
	message, _, err := database.TestingCreateSpaceMessageWithReferences(ctx, owner.ID, spaceID, nil, nil, []string{attachmentResult.Attachment.ID}, []string{promoted.ID}, "")
	if err != nil {
		t.Fatalf("CreateSpaceMessageWithReferences(attachment-only) error = %v", err)
	}
	reply, _, err := database.TestingCreateSpaceMessageWithReferences(ctx, owner.ID, spaceID, []MessageSpan{{Type: "text", Text: "reply"}}, nil, nil, nil, message.ID)
	if err != nil || reply.ReplyToMessageID != message.ID {
		t.Fatalf("reply = %#v, %v", reply, err)
	}
	messages, err := database.SpaceMessages(ctx, owner.ID, spaceID, 0, 20)
	if err != nil || len(messages) != 2 || len(messages[1].Attachments) != 1 || len(messages[1].LibraryItemIDs) != 1 {
		t.Fatalf("SpaceMessages() = %#v, %v", messages, err)
	}

	updated, err := database.UpdateLibraryItem(ctx, owner.ID, spaceID, completed.Item.ID, completed.Item.Version, completed.Item.DisplayName, "Red sunset over the water", []string{"travel", "coast"}, true, false)
	if err != nil {
		t.Fatalf("UpdateLibraryItem() error = %v", err)
	}
	assertLibraryRealtimeEvent(t, database, ctx, owner.ID, "library.item.updated", updated.ID)
	searchResults, err := database.LibraryItems(ctx, owner.ID, spaceID, LibraryItemQuery{Search: "sunset", Sort: "name", Direction: "asc"})
	if err != nil || len(searchResults) != 1 || searchResults[0].ID != updated.ID {
		t.Fatalf("search results = %#v, %v", searchResults, err)
	}
	favorites, err := database.LibraryItems(ctx, owner.ID, spaceID, LibraryItemQuery{Favorite: true})
	if err != nil || len(favorites) != 1 || favorites[0].ID != updated.ID {
		t.Fatalf("favorite results = %#v, %v", favorites, err)
	}
	documents, err := database.LibraryItems(ctx, owner.ID, spaceID, LibraryItemQuery{MediaType: "document", Sort: "name", Direction: "asc"})
	if err != nil || len(documents) != 2 || documents[0].DisplayName != "first.txt" || documents[1].DisplayName != "second.txt" {
		t.Fatalf("document results = %#v, %v", documents, err)
	}
	updated, err = database.UpdateLibraryItem(ctx, owner.ID, spaceID, updated.ID, updated.Version, updated.DisplayName, updated.Caption, updated.Tags, updated.Favorite, true)
	if err != nil {
		t.Fatalf("hide item error = %v", err)
	}
	visible, err := database.LibraryItems(ctx, owner.ID, spaceID, LibraryItemQuery{})
	if err != nil || len(visible) != 1 || visible[0].ID != promoted.ID {
		t.Fatalf("visible results = %#v, %v", visible, err)
	}
	hidden, err := database.LibraryItems(ctx, owner.ID, spaceID, LibraryItemQuery{Visibility: "hidden"})
	if err != nil || len(hidden) != 1 || hidden[0].ID != updated.ID {
		t.Fatalf("hidden results = %#v, %v", hidden, err)
	}

	bulkItems, err := database.BulkUpdateLibraryItems(ctx, owner.ID, spaceID, BulkLibraryItemOperation{Action: "unhide", Items: []LibraryItemVersion{{ID: updated.ID, Version: updated.Version}}})
	if err != nil || len(bulkItems) != 1 || bulkItems[0].Hidden {
		t.Fatalf("bulk unhide = %#v, %v", bulkItems, err)
	}
	updated = &bulkItems[0]
	if _, err := database.BulkUpdateLibraryItems(ctx, owner.ID, spaceID, BulkLibraryItemOperation{Action: "unfavorite", Items: []LibraryItemVersion{{ID: updated.ID, Version: updated.Version}, {ID: promoted.ID, Version: promoted.Version + 99}}}); !errors.Is(err, ErrLibraryConflict) {
		t.Fatalf("stale bulk update error = %v, want ErrLibraryConflict", err)
	}
	favorites, err = database.LibraryItems(ctx, owner.ID, spaceID, LibraryItemQuery{Favorite: true})
	if err != nil || len(favorites) != 1 || favorites[0].ID != updated.ID {
		t.Fatalf("failed bulk operation was not atomic: %#v, %v", favorites, err)
	}
	metadataItems := []LibraryItemVersion{{ID: updated.ID, Version: updated.Version}, {ID: promoted.ID, Version: promoted.Version}}
	bulkItems, err = database.BulkUpdateLibraryItems(ctx, owner.ID, spaceID, BulkLibraryItemOperation{Action: "add_tags", Items: metadataItems, Tags: []string{"Summer", "Coast"}})
	if err != nil || len(bulkItems) != 2 || !containsString(bulkItems[0].Tags, "Summer") || !containsString(bulkItems[1].Tags, "Summer") {
		t.Fatalf("bulk add tags = %#v, %v", bulkItems, err)
	}
	updated, promoted = libraryItemsByID(t, bulkItems, updated.ID, promoted.ID)
	bulkItems, err = database.BulkUpdateLibraryItems(ctx, owner.ID, spaceID, BulkLibraryItemOperation{Action: "remove_tags", Items: []LibraryItemVersion{{ID: updated.ID, Version: updated.Version}, {ID: promoted.ID, Version: promoted.Version}}, Tags: []string{"coast"}})
	if err != nil || containsString(bulkItems[0].Tags, "Coast") || containsString(bulkItems[1].Tags, "Coast") {
		t.Fatalf("bulk remove tags = %#v, %v", bulkItems, err)
	}
	updated, promoted = libraryItemsByID(t, bulkItems, updated.ID, promoted.ID)
	capturedAt := time.Date(2026, time.July, 4, 12, 30, 0, 0, time.UTC)
	bulkItems, err = database.BulkUpdateLibraryItems(ctx, owner.ID, spaceID, BulkLibraryItemOperation{Action: "set_date", Items: []LibraryItemVersion{{ID: updated.ID, Version: updated.Version}, {ID: promoted.ID, Version: promoted.Version}}, DateOverride: &capturedAt})
	if err != nil || bulkItems[0].DateOverride == nil || !bulkItems[0].DateOverride.Equal(capturedAt) || bulkItems[1].DateOverride == nil || !bulkItems[1].DateOverride.Equal(capturedAt) {
		t.Fatalf("bulk set date = %#v, %v", bulkItems, err)
	}
	updated, promoted = libraryItemsByID(t, bulkItems, updated.ID, promoted.ID)
	location := json.RawMessage(`{"name":"Big Sur","latitude":36.2704,"longitude":-121.8079}`)
	bulkItems, err = database.BulkUpdateLibraryItems(ctx, owner.ID, spaceID, BulkLibraryItemOperation{Action: "set_location", Items: []LibraryItemVersion{{ID: updated.ID, Version: updated.Version}, {ID: promoted.ID, Version: promoted.Version}}, LocationOverride: location})
	if err != nil || !strings.Contains(string(bulkItems[0].LocationOverride), "Big Sur") || !strings.Contains(string(bulkItems[1].LocationOverride), "Big Sur") {
		t.Fatalf("bulk set location = %#v, %v", bulkItems, err)
	}
	updated, promoted = libraryItemsByID(t, bulkItems, updated.ID, promoted.ID)
	bulkItems, err = database.BulkUpdateLibraryItems(ctx, owner.ID, spaceID, BulkLibraryItemOperation{Action: "clear_date", Items: []LibraryItemVersion{{ID: updated.ID, Version: updated.Version}, {ID: promoted.ID, Version: promoted.Version}}})
	if err != nil || bulkItems[0].DateOverride != nil || bulkItems[1].DateOverride != nil {
		t.Fatalf("bulk clear date = %#v, %v", bulkItems, err)
	}
	updated, promoted = libraryItemsByID(t, bulkItems, updated.ID, promoted.ID)
	bulkItems, err = database.BulkUpdateLibraryItems(ctx, owner.ID, spaceID, BulkLibraryItemOperation{Action: "clear_location", Items: []LibraryItemVersion{{ID: updated.ID, Version: updated.Version}, {ID: promoted.ID, Version: promoted.Version}}})
	if err != nil || string(bulkItems[0].LocationOverride) != "null" || string(bulkItems[1].LocationOverride) != "null" {
		t.Fatalf("bulk clear location = %#v, %v", bulkItems, err)
	}
	updated, promoted = libraryItemsByID(t, bulkItems, updated.ID, promoted.ID)
	structured, err := database.LibraryItems(ctx, owner.ID, spaceID, LibraryItemQuery{Search: `tag:travel type:document favorite:true after:2020-01-01`})
	if err != nil || len(structured) != 1 || structured[0].ID != updated.ID {
		t.Fatalf("structured search = %#v, %v", structured, err)
	}
	facets, err := database.LibraryFacets(ctx, owner.ID, spaceID, "")
	if err != nil || facets.Total != 2 || facets.Favorites != 1 || facets.Hidden != 0 || facets.RecentlyDeleted != 0 || len(facets.Tags) != 2 || len(facets.MediaTypes) != 1 || facets.MediaTypes[0].Value != "document" {
		t.Fatalf("LibraryFacets() = %#v, %v", facets, err)
	}
	transferItems, err := database.LibraryTransferItems(ctx, owner.ID, spaceID, []string{updated.ID, promoted.ID})
	if err != nil || len(transferItems) != 2 || transferItems[0].SHA256 != digest {
		t.Fatalf("LibraryTransferItems() = %#v, %v", transferItems, err)
	}
	if _, err := database.LibraryItemDownload(ctx, owner.ID, spaceID, updated.ID); err != nil {
		t.Fatalf("LibraryItemDownload(view tracking): %v", err)
	}
	viewed, err := database.LibraryItems(ctx, owner.ID, spaceID, LibraryItemQuery{Utility: "recently-viewed"})
	if err != nil || len(viewed) != 1 || viewed[0].ID != updated.ID {
		t.Fatalf("recently viewed utility = %#v, %v", viewed, err)
	}
	facets, err = database.LibraryFacets(ctx, owner.ID, spaceID, "")
	if err != nil || libraryFacetCount(facets.Utilities, "recently-viewed") != 1 || libraryFacetCount(facets.Utilities, "documents") != 2 {
		t.Fatalf("utility facets = %#v, %v", facets.Utilities, err)
	}
	testLibraryLifecycle(t, database, ctx, owner.ID, space, updated, promoted)
}
