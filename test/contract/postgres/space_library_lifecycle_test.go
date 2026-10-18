package db

import (
	"context"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"testing"
)

import ()

func testLibraryLifecycle(
	t *testing.T,
	database *Database,
	ctx context.Context,
	ownerID string,
	space *Space,
	updated, promoted *SpaceLibraryItem,
) {
	t.Helper()
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
}
