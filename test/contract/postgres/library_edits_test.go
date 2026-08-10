package db

import (
	"context"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestLibraryCopyOnWriteEditVersionsAndRevert(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("Edit Owner", "edit-owner@example.com", "password123")
	spaceID := createTestSpace(t, database, ctx, owner.ID, "Edits").ID
	item := createPeopleTestImage(t, database, owner.ID, spaceID, "portrait.jpg", "e")

	firstDefinition := DefaultLibraryEditDefinition()
	firstDefinition.Rotation = 90
	firstDefinition.Contrast = 1.2
	firstDefinition.Exposure = .5
	firstDefinition.Straighten = 2.5
	firstDefinition.Markup = []LibraryMarkupElement{{Kind: "text", X: .1, Y: .1, Color: "#ff3b30", LineWidth: .012, Opacity: 1, Text: "Misty"}}
	first, err := database.CreateLibraryEditVersion(ctx, owner.ID, spaceID, item.ID, item.Version, firstDefinition)
	if err != nil || first.Edit == nil || first.Item.CurrentEditVersionID != first.Edit.ID || first.Item.Version != item.Version+1 {
		t.Fatalf("first edit = %#v, %v", first, err)
	}
	secondDefinition := firstDefinition
	secondDefinition.Saturation = 0.5
	secondDefinition.Crop = &LibraryCrop{X: 0.1, Y: 0.1, Width: 0.8, Height: 0.8}
	second, err := database.CreateLibraryEditVersion(ctx, owner.ID, spaceID, item.ID, first.Item.Version, secondDefinition)
	if err != nil || second.Edit == nil || second.Edit.ParentVersionID != first.Edit.ID || second.Edit.VersionNumber != 2 {
		t.Fatalf("second edit = %#v, %v", second, err)
	}
	if _, err := database.CreateLibraryEditVersion(ctx, owner.ID, spaceID, item.ID, first.Item.Version, firstDefinition); !errors.Is(err, ErrLibraryConflict) {
		t.Fatalf("stale edit error = %v", err)
	}
	versions, err := database.LibraryEditVersions(ctx, owner.ID, spaceID, item.ID)
	if err != nil || len(versions) != 2 || !versions[0].IsCurrent || versions[0].ID != second.Edit.ID {
		t.Fatalf("versions = %#v, %v", versions, err)
	}
	selected, err := database.SelectLibraryEditVersion(ctx, owner.ID, spaceID, item.ID, first.Edit.ID, second.Item.Version)
	if err != nil || selected.Item.CurrentEditVersionID != first.Edit.ID {
		t.Fatalf("select version = %#v, %v", selected, err)
	}
	if err := database.DeleteLibraryEditVersion(ctx, owner.ID, spaceID, item.ID, second.Edit.ID); err != nil {
		t.Fatalf("delete inactive edit error = %v", err)
	}
	if err := database.DeleteLibraryEditVersion(ctx, owner.ID, spaceID, item.ID, first.Edit.ID); !errors.Is(err, ErrLibraryConflict) {
		t.Fatalf("delete current edit error = %v", err)
	}
	reverted, err := database.SelectLibraryEditVersion(ctx, owner.ID, spaceID, item.ID, "", selected.Item.Version)
	if err != nil || reverted.Item.CurrentEditVersionID != "" {
		t.Fatalf("revert to original = %#v, %v", reverted, err)
	}
	if err := database.DeleteLibraryEditVersion(ctx, owner.ID, spaceID, item.ID, first.Edit.ID); err != nil {
		t.Fatalf("delete reverted edit error = %v", err)
	}

	invalid := DefaultLibraryEditDefinition()
	invalid.Crop = &LibraryCrop{X: 0.8, Y: 0, Width: 0.5, Height: 1}
	if _, err := database.CreateLibraryEditVersion(ctx, owner.ID, spaceID, item.ID, reverted.Item.Version, invalid); !errors.Is(err, ErrLibraryInvalid) {
		t.Fatalf("invalid crop error = %v", err)
	}
	invalid = DefaultLibraryEditDefinition()
	invalid.Trim = &LibraryTrim{Start: 1, End: 2}
	if _, err := database.CreateLibraryEditVersion(ctx, owner.ID, spaceID, item.ID, reverted.Item.Version, invalid); !errors.Is(err, ErrLibraryInvalid) {
		t.Fatalf("image trim error = %v", err)
	}

	otherSpace, _ := database.CreateSpace(ctx, owner.ID, "Other edit domain")
	if _, err := database.LibraryEditVersions(ctx, owner.ID, otherSpace.ID, item.ID); !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("cross-Space versions error = %v", err)
	}
}
