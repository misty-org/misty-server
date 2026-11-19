package db

import (
	"database/sql"
	"errors"
	"testing"

	. "github.com/misty-org/misty-server/internal/platform/db"
)

// Decks and workbooks live exactly as drawings do: every member edits, the creator and the
// owner delete, a delete purges the room before the row goes, and their events reach only
// those who may see them.
func TestSpaceMadeItemsAccessLifecycleAndProjection(t *testing.T) {
	for _, kindName := range []string{SpaceItemDeck, SpaceItemWorkbook} {
		t.Run(kindName, func(t *testing.T) {
			fixture := newNoteFixture(t, "made-"+kindName)
			database, ctx := fixture.database, fixture.ctx
			kind, _ := SpaceItemKindNamed(kindName)

			item, err := database.CreateSpaceMadeItem(ctx, kindName, fixture.creator, fixture.spaceID, "")
			if err != nil {
				t.Fatal(err)
			}
			if item.Title != kind.DefaultTitle || item.Role != DrawingRoleCreator || !item.CanDelete ||
				item.Visibility != VisibilityPublic {
				t.Fatalf("created %s = %#v", kindName, item)
			}

			for name, userID := range map[string]string{"creator": fixture.creator, "member": fixture.member, "owner": fixture.owner} {
				access, err := database.MadeItemAccessFor(ctx, kindName, userID, item.ID)
				if err != nil || !access.CanView || !access.CanEdit {
					t.Fatalf("%s access = %#v %v", name, access, err)
				}
			}
			if access, _ := database.MadeItemAccessFor(ctx, kindName, fixture.member, item.ID); access.CanDelete {
				t.Fatal("an ordinary member can delete")
			}
			if err := database.DeleteSpaceMadeItem(ctx, kindName, fixture.member, item.ID); !errors.Is(err, ErrSpaceNotFound) {
				t.Fatalf("member delete = %v", err)
			}

			renamed, err := database.RenameSpaceMadeItem(ctx, kindName, fixture.member, item.ID, "Quarterly review")
			if err != nil || renamed.Title != "Quarterly review" || renamed.Role != DrawingRoleEditor {
				t.Fatalf("renamed = %#v %v", renamed, err)
			}
			commands, err := database.PendingMadeItemControlCommands(ctx, kindName, 10)
			if err != nil || len(commands) != 1 || commands[0].Command != "title" || commands[0].ItemID != item.ID {
				t.Fatalf("rename commands = %#v %v", commands, err)
			}
			if err := database.MarkMadeItemControlDelivered(ctx, kindName, commands[0].ID); err != nil {
				t.Fatal(err)
			}

			// The room's projection: newer revisions only, then search finds the text.
			applied, err := database.ApplySpaceMadeItemProjection(ctx, SpaceMadeItemProjection{
				Kind: kindName, ItemID: item.ID, Revision: 2, Title: "Quarterly review",
				PlainText: "revenue grew in the northern region", ContentBytes: 4096, HistoryBytes: 1024, Count: 7,
			})
			if err != nil || !applied {
				t.Fatalf("projection applied = %v %v", applied, err)
			}
			if again, err := database.ApplySpaceMadeItemProjection(ctx, SpaceMadeItemProjection{
				Kind: kindName, ItemID: item.ID, Revision: 2, Title: "Stale", ContentBytes: 1,
			}); err != nil || again {
				t.Fatalf("a repeated revision applied = %v %v", again, err)
			}
			read, err := database.SpaceMadeItemByID(ctx, kindName, fixture.member, item.ID)
			if err != nil || read.PlainText != "revenue grew in the northern region" || read.ContentBytes != 4096 ||
				read.HistoryBytes != 1024 || read.OverLimit {
				t.Fatalf("read after projection = %#v %v", read, err)
			}
			if kindName == SpaceItemDeck && (read.SlideCount == nil || *read.SlideCount != 7) {
				t.Fatalf("deck slide count = %v", read.SlideCount)
			}
			if kindName == SpaceItemWorkbook && (read.CellCount == nil || *read.CellCount != 7) {
				t.Fatalf("workbook cell count = %v", read.CellCount)
			}
			var counted int64
			if err := database.Conn.QueryRow(`SELECT logical_bytes FROM space_storage_contributions
				WHERE source_kind='item_content' AND source_id=$1 AND state='active'`, item.ID).Scan(&counted); err != nil || counted != 5120 {
				t.Fatalf("stored bytes counted = %d %v", counted, err)
			}
			hits, err := database.SearchSpaceKinds(ctx, fixture.member, "northern", fixture.spaceID, map[string]bool{kindName: true}, 10)
			if err != nil || len(hits) != 1 || hits[0].Kind != kindName || hits[0].ID != item.ID {
				t.Fatalf("search hits = %#v %v", hits, err)
			}

			// Private: gone for the member, everywhere.
			if err := database.SetSpaceItemVisibility(ctx, fixture.creator, fixture.spaceID, kindName, item.ID, VisibilityPrivate); err != nil {
				t.Fatal(err)
			}
			if _, err := database.SpaceMadeItemByID(ctx, kindName, fixture.member, item.ID); !errors.Is(err, ErrSpaceNotFound) {
				t.Fatalf("member read of a private item = %v", err)
			}
			if listed, err := database.AccessibleSpaceMadeItems(ctx, kindName, fixture.member, fixture.spaceID); err != nil || len(listed) != 0 {
				t.Fatalf("member list of a private item = %#v %v", listed, err)
			}
			if hits, _ := database.SearchSpaceKinds(ctx, fixture.member, "northern", fixture.spaceID, map[string]bool{kindName: true}, 10); len(hits) != 0 {
				t.Fatalf("member search found a private item: %#v", hits)
			}
			commands, err = database.PendingMadeItemControlCommands(ctx, kindName, 10)
			if err != nil || len(commands) != 1 || commands[0].Command != "acl" {
				t.Fatalf("visibility commands = %#v %v", commands, err)
			}
			_ = database.MarkMadeItemControlDelivered(ctx, kindName, commands[0].ID)
			if err := database.SetSpaceItemVisibility(ctx, fixture.creator, fixture.spaceID, kindName, item.ID, VisibilityPublic); err != nil {
				t.Fatal(err)
			}
			commands, _ = database.PendingMadeItemControlCommands(ctx, kindName, 10)
			for _, command := range commands {
				_ = database.MarkMadeItemControlDelivered(ctx, kindName, command.ID)
			}

			// Losing a member moves every room's ACL on.
			if err := database.RemoveSpaceMember(ctx, fixture.owner, fixture.spaceID, fixture.member); err != nil {
				t.Fatal(err)
			}
			if access, _ := database.MadeItemAccessFor(ctx, kindName, fixture.member, item.ID); access.CanView {
				t.Fatal("a removed member still sees the item")
			}
			commands, err = database.PendingMadeItemControlCommands(ctx, kindName, 10)
			if err != nil || len(commands) != 1 || commands[0].Command != "acl" {
				t.Fatalf("membership loss commands = %#v %v", commands, err)
			}
			_ = database.MarkMadeItemControlDelivered(ctx, kindName, commands[0].ID)

			// Delete, purge, and the storage it counted is released.
			if err := database.DeleteSpaceMadeItem(ctx, kindName, fixture.owner, item.ID); err != nil {
				t.Fatal(err)
			}
			events, _, err := database.SpaceEventsAfter(ctx, fixture.creator, 0, 200)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range []string{"created", "updated", "deleted"} {
				if !containsSpaceEvent(events, kind.EventFamily+event, item.ID) {
					t.Fatalf("creator's events lack %s%s", kind.EventFamily, event)
				}
			}
			if purged, err := database.PurgeDeletedMadeItems(ctx, kindName, 10); err != nil || purged != 0 {
				t.Fatalf("purged before the room confirmed = %d %v", purged, err)
			}
			commands, err = database.PendingMadeItemControlCommands(ctx, kindName, 10)
			if err != nil || len(commands) != 1 || commands[0].Command != "purge" {
				t.Fatalf("delete commands = %#v %v", commands, err)
			}
			if err := database.MarkMadeItemControlDelivered(ctx, kindName, commands[0].ID); err != nil {
				t.Fatal(err)
			}
			if purged, err := database.PurgeDeletedMadeItems(ctx, kindName, 10); err != nil || purged != 1 {
				t.Fatalf("purged = %d %v", purged, err)
			}
			var remaining string
			if err := database.Conn.QueryRow(`SELECT id FROM `+kind.Table+` WHERE id=$1`, item.ID).Scan(&remaining); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("purged row = %v", err)
			}
			var state string
			if err := database.Conn.QueryRow(`SELECT state FROM space_storage_contributions
				WHERE source_kind='item_content' AND source_id=$1`, item.ID).Scan(&state); err != nil || state != "released" {
				t.Fatalf("stored bytes after purge = %q %v", state, err)
			}
		})
	}
}

// History reads need the item's view access, in the item's own Space, for every kind.
func TestSpaceItemAccessForHistory(t *testing.T) {
	fixture := newNoteFixture(t, "made-history")
	database, ctx := fixture.database, fixture.ctx
	deck, err := database.CreateSpaceMadeItem(ctx, SpaceItemDeck, fixture.creator, fixture.spaceID, "Plan")
	if err != nil {
		t.Fatal(err)
	}
	drawing, err := database.CreateSpaceDrawing(ctx, fixture.creator, fixture.spaceID, "Sketch")
	if err != nil {
		t.Fatal(err)
	}
	for kind, id := range map[string]string{SpaceItemDeck: deck.ID, SpaceItemDrawing: drawing.ID, SpaceItemNote: fixture.note.ID} {
		access, limits, err := database.SpaceItemAccess(ctx, kind, fixture.member, fixture.spaceID, id)
		if err != nil || !access.CanView || limits.HistoryDays != nil {
			t.Fatalf("%s history access = %#v %#v %v", kind, access, limits, err)
		}
		if _, _, err := database.SpaceItemAccess(ctx, kind, fixture.member, "space_elsewhere", id); !errors.Is(err, ErrSpaceNotFound) {
			t.Fatalf("%s in another Space = %v", kind, err)
		}
	}
	if _, _, err := database.SpaceItemAccess(ctx, "image", fixture.member, fixture.spaceID, deck.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("an unregistered kind = %v", err)
	}
	// A drawing's text is searchable once its room reports it.
	text := "sequence diagram for checkout"
	if applied, err := database.ApplySpaceDrawingActivityProjection(ctx, SpaceDrawingActivity{
		DrawingID: drawing.ID, Revision: 1, PlainText: &text,
	}); err != nil || !applied {
		t.Fatalf("drawing projection = %v %v", applied, err)
	}
	hits, err := database.SearchSpaceKinds(ctx, fixture.member, "checkout", fixture.spaceID, map[string]bool{SpaceItemDrawing: true}, 10)
	if err != nil || len(hits) != 1 || hits[0].Kind != SpaceItemDrawing {
		t.Fatalf("drawing search = %#v %v", hits, err)
	}
}
