package db

import (
	"context"
	"strings"
	"time"
)


import (
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestParseLibrarySearchRejectsInvalidStructuredValues(t *testing.T) {
	if _, err := TestingParseLibrarySearch("type:executable"); !errors.Is(err, ErrLibraryInvalid) {
		t.Fatalf("type error = %v", err)
	}
	if _, err := TestingParseLibrarySearch("after:tomorrow"); !errors.Is(err, ErrLibraryInvalid) {
		t.Fatalf("date error = %v", err)
	}
	parsed, err := TestingParseLibrarySearch(`summer trip album:"Road trip" hidden:false`)
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
	useResourceAdapterFixture(t, database)
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

func TestLibraryQuotaReservationsDrawOnEachAccountsAllowance(t *testing.T) {
	database := openTestDatabase(t)
	useResourceAdapterFixture(t, database)
	ctx := context.Background()
	owner, _ := database.CreateUser("quota-owner@example.com", "password123")
	member, _ := database.CreateUser("quota-member@example.com", "password123")
	spaceID := createTestSpace(t, database, ctx, owner.ID, "Quota").ID
	invite, err := database.TestingInviteToSpace(ctx, owner.ID, spaceID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("b", 64)
	// Storage belongs to the account that uploads it; a shared Space has no pool.
	ownerHalf := reserveQuotaBytes(t, database, ctx, owner.ID, spaceID, "quota-owner", FreeStorageBytes/2)
	memberFull := reserveQuotaBytes(t, database, ctx, member.ID, spaceID, "quota-member", FreeStorageBytes)
	if _, err := database.CreateLibraryUpload(ctx, member.ID, spaceID, "library", "extra.bin", "application/octet-stream", 1, digest, "library/extraobject", "extra-token", time.Now().Add(time.Hour)); !errors.Is(err, ErrLibraryQuota) {
		t.Fatalf("an account over its own allowance = %v, want ErrLibraryQuota", err)
	}
	ownerExtra := reserveQuotaBytes(t, database, ctx, owner.ID, spaceID, "quota-owner-extra", 1)
	releaseQuota(t, database, ctx, ownerExtra)
	releaseQuota(t, database, ctx, ownerHalf)
	releaseQuota(t, database, ctx, memberFull)
	usage, _ := database.SpaceStorageUsage(ctx, member.ID, spaceID)
	if usage.ReservedBytes != 0 || usage.RemainingBytes != FreeStorageBytes {
		t.Fatalf("released reservation usage = %#v", usage)
	}
	// Leave room for exactly one more maximum-size Library upload, so two
	// concurrent reservations by one account resolve to one winner and one denial.
	raceChunk := DefaultLibraryMaxFileBytes
	baseReservations := reserveQuotaBytes(t, database, ctx, owner.ID, spaceID, "quota-base", FreeStorageBytes-raceChunk)

	type reservationResult struct {
		token  string
		upload *LibraryUpload
		err    error
	}
	start := make(chan struct{})
	results := make(chan reservationResult, 2)
	reserve := func(token, key string) {
		<-start
		next, reserveErr := database.CreateLibraryUpload(ctx, owner.ID, spaceID, "library", key+".bin", "application/octet-stream", raceChunk, digest, "library/"+key, token, time.Now().Add(time.Hour))
		results <- reservationResult{token: token, upload: next, err: reserveErr}
	}
	go reserve("race-token-a", "race-a")
	go reserve("race-token-b", "race-b")
	close(start)
	first, second := <-results, <-results
	succeeded, denied := 0, 0
	for _, result := range []reservationResult{first, second} {
		switch {
		case result.err == nil:
			succeeded++
			if err := database.RejectLibraryUpload(ctx, owner.ID, spaceID, result.upload.ID, result.token, "invalid", "test_cleanup"); err != nil {
				t.Fatal(err)
			}
		case errors.Is(result.err, ErrLibraryQuota):
			denied++
		default:
			t.Fatalf("concurrent reservation error = %v", result.err)
		}
	}
	if succeeded != 1 || denied != 1 {
		t.Fatalf("concurrent account quota results: succeeded=%d denied=%d", succeeded, denied)
	}
	releaseQuota(t, database, ctx, baseReservations)
}
