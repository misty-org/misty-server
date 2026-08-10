package db

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func testLibrarySharingAndLifecycle(
	t *testing.T,
	database *Database,
	ctx context.Context,
	ownerID string,
	space *Space,
	digest string,
	updated, promoted *SpaceLibraryItem,
	album *LibraryAlbum,
	memoryID, tripID string,
) {
	t.Helper()
	destination, err := database.CreateSpace(ctx, ownerID, "Transfer destination")
	if err != nil {
		t.Fatalf("CreateSpace(transfer destination): %v", err)
	}
	shared, err := database.CreateLibraryGrant(ctx, ownerID, space.ID, updated.ID, destination.ID)
	if err != nil || shared.SourceItemID != updated.ID || shared.DestinationSpaceID != destination.ID {
		t.Fatalf("CreateLibraryGrant() = %#v, %v", shared, err)
	}
	references, err := database.LibrarySharedReferences(ctx, ownerID, destination.ID)
	if err != nil || len(references) != 1 || references[0].ID != shared.ID {
		t.Fatalf("LibrarySharedReferences() = %#v, %v", references, err)
	}
	sharedDownload, err := database.LibrarySharedReferenceDownload(ctx, ownerID, destination.ID, shared.ID)
	if err != nil || sharedDownload.SHA256 != digest {
		t.Fatalf("LibrarySharedReferenceDownload() = %#v, %v", sharedDownload, err)
	}

	importUpload, err := database.CreateLibraryUpload(ctx, ownerID, destination.ID, "library", "imported.txt", "text/plain", 100, digest, "library/importobject", "import-token", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("CreateLibraryUpload(import): %v", err)
	}
	if _, err := database.SetLibraryUploadState(ctx, ownerID, destination.ID, importUpload.ID, "import-token", "initiated", "uploaded_unverified"); err != nil {
		t.Fatalf("SetLibraryUploadState(import): %v", err)
	}
	imported, err := database.CompleteLibraryUpload(ctx, ownerID, destination.ID, importUpload.ID, "import-token", 100, digest, "text/plain; charset=utf-8", nil)
	if err != nil || imported.Item == nil {
		t.Fatalf("CompleteLibraryUpload(import) = %#v, %v", imported, err)
	}
	importRecord, err := database.RecordLibraryImport(ctx, ownerID, space.ID, updated.ID, destination.ID, imported.Item.ID, importUpload.ID, 100)
	if err != nil || importRecord.State != "ready" || importRecord.DestinationItemID != imported.Item.ID {
		t.Fatalf("RecordLibraryImport() = %#v, %v", importRecord, err)
	}
	sourceHistory, err := database.LibraryImportHistory(ctx, ownerID, space.ID, 10)
	if err != nil || len(sourceHistory) != 1 || sourceHistory[0].Direction != "outgoing" || sourceHistory[0].CounterpartSpaceName != destination.Name || sourceHistory[0].ItemID != updated.ID {
		t.Fatalf("source LibraryImportHistory() = %#v, %v", sourceHistory, err)
	}
	destinationHistory, err := database.LibraryImportHistory(ctx, ownerID, destination.ID, 10)
	if err != nil || len(destinationHistory) != 1 || destinationHistory[0].Direction != "incoming" || destinationHistory[0].CounterpartSpaceName != space.Name || destinationHistory[0].ItemID != imported.Item.ID {
		t.Fatalf("destination LibraryImportHistory() = %#v, %v", destinationHistory, err)
	}
	importedUtility, err := database.LibraryItems(ctx, ownerID, destination.ID, LibraryItemQuery{Utility: "imports"})
	if err != nil || len(importedUtility) != 1 || importedUtility[0].ID != imported.Item.ID {
		t.Fatalf("imports utility = %#v, %v", importedUtility, err)
	}

	pins, err := database.SetLibraryPinnedCollections(ctx, ownerID, space.ID, []LibraryPinTarget{{Kind: "system", ID: "favorites"}, {Kind: "album", ID: album.ID}, {Kind: "memory", ID: memoryID}, {Kind: "trip", ID: tripID}})
	if err != nil || len(pins) != 4 || pins[0].Position != 0 || pins[0].TargetID != "favorites" || pins[3].Position != 3 || pins[3].TargetID != tripID {
		t.Fatalf("SetLibraryPinnedCollections() = %#v, %v", pins, err)
	}
	listedPins, err := database.LibraryPinnedCollections(ctx, ownerID, space.ID)
	if err != nil || len(listedPins) != 4 || listedPins[1].TargetID != album.ID {
		t.Fatalf("LibraryPinnedCollections() = %#v, %v", listedPins, err)
	}
	if _, err := database.SetLibraryPinnedCollections(ctx, ownerID, destination.ID, []LibraryPinTarget{{Kind: "album", ID: album.ID}}); !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("cross-Space album pin error = %v, want ErrLibraryNotFound", err)
	}
	if err := database.RevokeLibraryGrant(ctx, ownerID, space.ID, shared.GrantID, shared.Version); err != nil {
		t.Fatalf("RevokeLibraryGrant(): %v", err)
	}
	references, err = database.LibrarySharedReferences(ctx, ownerID, destination.ID)
	if err != nil || len(references) != 0 {
		t.Fatalf("references after revoke = %#v, %v", references, err)
	}

	bulkItems, err := database.BulkUpdateLibraryItems(ctx, ownerID, space.ID, BulkLibraryItemOperation{Action: "trash", Items: []LibraryItemVersion{{ID: updated.ID, Version: updated.Version}, {ID: promoted.ID, Version: promoted.Version}}})
	if err != nil || len(bulkItems) != 2 || bulkItems[0].LifecycleState != "trash" || bulkItems[1].LifecycleState != "trash" {
		t.Fatalf("bulk trash = %#v, %v", bulkItems, err)
	}
	deleted, err := database.LibraryItems(ctx, ownerID, space.ID, LibraryItemQuery{Collection: "recently-deleted", Visibility: "all"})
	if err != nil || len(deleted) != 2 {
		t.Fatalf("recently deleted = %#v, %v", deleted, err)
	}
	bulkItems, err = database.BulkUpdateLibraryItems(ctx, ownerID, space.ID, BulkLibraryItemOperation{Action: "restore", Items: []LibraryItemVersion{{ID: bulkItems[0].ID, Version: bulkItems[0].Version}, {ID: bulkItems[1].ID, Version: bulkItems[1].Version}}})
	if err != nil || len(bulkItems) != 2 || bulkItems[0].LifecycleState != "ready" || bulkItems[1].LifecycleState != "ready" {
		t.Fatalf("bulk restore = %#v, %v", bulkItems, err)
	}
	recovered, err := database.LibraryItems(ctx, ownerID, space.ID, LibraryItemQuery{Utility: "recovered"})
	if err != nil || len(recovered) != 2 {
		t.Fatalf("recovered utility = %#v, %v", recovered, err)
	}
	recentlySaved, err := database.LibraryItems(ctx, ownerID, space.ID, LibraryItemQuery{Utility: "recently-saved"})
	if err != nil || len(recentlySaved) != 1 || recentlySaved[0].ID != promoted.ID {
		t.Fatalf("recently saved utility = %#v, %v", recentlySaved, err)
	}
	if err := database.DeleteLibraryAlbum(ctx, ownerID, space.ID, album.ID, album.Version-1); !errors.Is(err, ErrLibraryConflict) {
		t.Fatalf("stale DeleteLibraryAlbum() error = %v", err)
	}
	if err := database.DeleteLibraryAlbum(ctx, ownerID, space.ID, album.ID, album.Version); err != nil {
		t.Fatalf("DeleteLibraryAlbum() error = %v", err)
	}
}
