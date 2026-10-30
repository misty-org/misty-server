package db

import (
	"context"
	"strings"
	"time"
)

import (
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/db"
)

func TestParseLibrarySearchRejectsInvalidStructuredValues(t *testing.T) {
	if _, err := ParseLibrarySearch("type:executable"); !errors.Is(err, ErrLibraryInvalid) {
		t.Fatalf("type error = %v", err)
	}
	if _, err := ParseLibrarySearch("after:tomorrow"); !errors.Is(err, ErrLibraryInvalid) {
		t.Fatalf("date error = %v", err)
	}
	parsed, err := ParseLibrarySearch(`summer trip album:"Road trip" hidden:false`)
	if err != nil || parsed.Text != "summer trip" || parsed.Album != "Road trip" || parsed.Hidden == nil || *parsed.Hidden {
		t.Fatalf("parsed search = %#v, %v", parsed, err)
	}
}

func assertLibraryRealtimeEvent(t *testing.T, database *Database, ctx context.Context, userID, eventType, entityID string) {
	t.Helper()
	events, _, err := database.SpaceEventsAfter(ctx, userID, 0, 500)
	if err != nil {
		t.Fatalf("SpaceEventsAfter(%s): %v", eventType, err)
	}
	for _, event := range events {
		if event.EventType == eventType && event.EntityID == entityID {
			return
		}
	}
	t.Fatalf("SpaceEventsAfter() missing %s for %s", eventType, entityID)
}

func TestMissingDeduplicationObjectCanBeReplacedByNewUpload(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("library-repair-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	spaceID := createTestSpace(t, database, ctx, owner.ID, "Library repair").ID
	digest := strings.Repeat("b", 64)

	first, err := database.CreateLibraryUpload(ctx, owner.ID, spaceID, "library", "first.jpg", "image/jpeg", 128, digest, "library/originalobject", "first-token", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetLibraryUploadState(ctx, owner.ID, spaceID, first.ID, "first-token", "initiated", "uploaded_unverified"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CompleteLibraryUpload(ctx, owner.ID, spaceID, first.ID, "first-token", 128, digest, "image/jpeg", nil); err != nil {
		t.Fatal(err)
	}

	replacement, err := database.CreateLibraryUpload(ctx, owner.ID, spaceID, "library", "replacement.jpg", "image/jpeg", 128, digest, "library/replacementobject", "replacement-token", time.Now().Add(time.Hour))
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
	if err != nil || completed.Item == nil || completed.DiscardObjectKey != "" {
		t.Fatalf("CompleteLibraryUpload() = %#v, %v", completed, err)
	}
	download, err := database.LibraryItemDownload(ctx, owner.ID, spaceID, completed.Item.ID)
	if err != nil || download.ObjectKey != "library/replacementobject" {
		t.Fatalf("LibraryItemDownload() = %#v, %v", download, err)
	}
}

func libraryItemsByID(t *testing.T, items []SpaceLibraryItem, firstID, secondID string) (*SpaceLibraryItem, *SpaceLibraryItem) {
	t.Helper()
	var first, second *SpaceLibraryItem
	for index := range items {
		switch items[index].ID {
		case firstID:
			first = &items[index]
		case secondID:
			second = &items[index]
		}
	}
	if first == nil || second == nil {
		t.Fatalf("missing items %q and %q in %#v", firstID, secondID, items)
	}
	return first, second
}

func libraryFacetCount(facets []LibrarySearchFacet, value string) int {
	for _, facet := range facets {
		if facet.Value == value {
			return facet.Count
		}
	}
	return 0
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
