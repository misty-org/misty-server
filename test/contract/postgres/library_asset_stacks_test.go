package db

import (
	"context"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestLibraryAssetStacksValidateAuthorizeAndPreventOverlap(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("Stack Owner", "stack-owner@example.com", "password123")
	outsider, _ := database.CreateUser("Stack Outsider", "stack-outsider@example.com", "password123")
	spaceID := createTestSpace(t, database, ctx, owner.ID, "Asset stacks").ID
	still := createAssetStackTestItem(t, database, owner.ID, spaceID, "IMG_0100.HEIC", "image/heic", "1")
	motion := createAssetStackTestItem(t, database, owner.ID, spaceID, "IMG_0100.MOV", "video/quicktime", "2")

	created, err := database.CreateLibraryAssetStack(ctx, owner.ID, spaceID, CreateLibraryAssetStack{
		Kind: "live_photo", CoverItemID: still.ID, MotionItemID: motion.ID,
		Members: []LibraryAssetStackMember{{ItemID: still.ID, Role: "still", Position: 0}, {ItemID: motion.ID, Role: "motion", Position: 1}},
	})
	if err != nil || created.Kind != "live_photo" || len(created.Members) != 2 {
		t.Fatalf("CreateLibraryAssetStack() = %#v, %v", created, err)
	}
	created, err = database.UpdateLibraryAssetStack(ctx, owner.ID, spaceID, created.ID, created.Version, created.Title, created.CoverItemID, "bounce")
	if err != nil || created.Effect != "bounce" {
		t.Fatalf("set Live Photo effect = %#v, %v", created, err)
	}
	if _, err := database.CreateLibraryAssetStack(ctx, owner.ID, spaceID, CreateLibraryAssetStack{
		Kind: "live_photo", CoverItemID: still.ID, MotionItemID: motion.ID,
		Members: []LibraryAssetStackMember{{ItemID: still.ID, Role: "still", Position: 0}, {ItemID: motion.ID, Role: "motion", Position: 1}},
	}); err != ErrLibraryConflict {
		t.Fatalf("overlap error = %v, want conflict", err)
	}
	if _, err := database.LibraryAssetStacks(ctx, outsider.ID, spaceID); err == nil {
		t.Fatal("outsider listed asset stacks")
	}
	listed, err := database.LibraryItems(ctx, owner.ID, spaceID, LibraryItemQuery{})
	if err != nil || len(listed) != 1 || listed[0].ID != still.ID {
		t.Fatalf("collapsed LibraryItems() = %#v, %v", listed, err)
	}
	livePhotos, err := database.LibraryItems(ctx, owner.ID, spaceID, LibraryItemQuery{MediaType: "live-photos"})
	if err != nil || len(livePhotos) != 1 || livePhotos[0].ID != still.ID {
		t.Fatalf("Live Photos query = %#v, %v", livePhotos, err)
	}
	facets, err := database.LibraryFacets(ctx, owner.ID, spaceID, "")
	if err != nil || facets.Total != 1 || !hasLibraryFacet(facets.MediaTypes, "live-photos", 1) {
		t.Fatalf("collapsed facets = %#v, %v", facets, err)
	}
	if err := database.DeleteLibraryAssetStack(ctx, owner.ID, spaceID, created.ID, created.Version); err != nil {
		t.Fatalf("DeleteLibraryAssetStack() = %v", err)
	}
	stacks, err := database.LibraryAssetStacks(ctx, owner.ID, spaceID)
	if err != nil || len(stacks) != 0 {
		t.Fatalf("LibraryAssetStacks() after delete = %#v, %v", stacks, err)
	}
	listed, err = database.LibraryItems(ctx, owner.ID, spaceID, LibraryItemQuery{})
	if err != nil || len(listed) != 2 {
		t.Fatalf("ungrouped LibraryItems() = %#v, %v", listed, err)
	}
}

func hasLibraryFacet(facets []LibrarySearchFacet, value string, count int) bool {
	for _, facet := range facets {
		if facet.Value == value && facet.Count == count {
			return true
		}
	}
	return false
}

func TestLibraryAssetStacksSupportRAWPairsAndBursts(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("Stack Formats", "stack-formats@example.com", "password123")
	spaceID := createTestSpace(t, database, ctx, owner.ID, "Stack formats").ID
	raw := createAssetStackTestItem(t, database, owner.ID, spaceID, "DSC_1000.CR3", "application/octet-stream", "3")
	jpeg := createAssetStackTestItem(t, database, owner.ID, spaceID, "DSC_1000.JPG", "image/jpeg", "4")
	if _, err := database.CreateLibraryAssetStack(ctx, owner.ID, spaceID, CreateLibraryAssetStack{
		Kind: "raw_pair", CoverItemID: jpeg.ID,
		Members: []LibraryAssetStackMember{{ItemID: jpeg.ID, Role: "alternate", Position: 0}, {ItemID: raw.ID, Role: "raw", Position: 1}},
	}); err != nil {
		t.Fatalf("create RAW pair = %v", err)
	}
	burstA := createAssetStackTestItem(t, database, owner.ID, spaceID, "IMG_BURST001.JPG", "image/jpeg", "5")
	burstB := createAssetStackTestItem(t, database, owner.ID, spaceID, "IMG_BURST002.JPG", "image/jpeg", "6")
	burst, err := database.CreateLibraryAssetStack(ctx, owner.ID, spaceID, CreateLibraryAssetStack{
		Kind: "burst", CoverItemID: burstA.ID,
		Members: []LibraryAssetStackMember{{ItemID: burstA.ID, Role: "burst_frame", Position: 0}, {ItemID: burstB.ID, Role: "burst_frame", Position: 1}},
	})
	if err != nil {
		t.Fatalf("create burst = %v", err)
	}
	burst, err = database.UpdateLibraryAssetStack(ctx, owner.ID, spaceID, burst.ID, burst.Version, "Best burst", burstB.ID, "still")
	if err != nil || burst.CoverItemID != burstB.ID || burst.Title != "Best burst" {
		t.Fatalf("update burst = %#v, %v", burst, err)
	}
	rawResults, err := database.LibraryItems(ctx, owner.ID, spaceID, LibraryItemQuery{MediaType: "raw"})
	if err != nil || len(rawResults) != 1 || rawResults[0].ID != jpeg.ID {
		t.Fatalf("RAW query = %#v, %v", rawResults, err)
	}
	burstResults, err := database.LibraryItems(ctx, owner.ID, spaceID, LibraryItemQuery{MediaType: "bursts"})
	if err != nil || len(burstResults) != 1 || burstResults[0].ID != burstB.ID {
		t.Fatalf("burst query = %#v, %v", burstResults, err)
	}
}

func createAssetStackTestItem(t *testing.T, database *Database, userID, spaceID, filename, mime, digestCharacter string) *SpaceLibraryItem {
	t.Helper()
	digest := strings.Repeat(digestCharacter, 64)
	token := "stack-token-" + digestCharacter
	upload, err := database.CreateLibraryUpload(context.Background(), userID, spaceID, "library", filename, mime, 128, digest, "library/asset-stack-"+digestCharacter, token, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("initiate %s = %v", filename, err)
	}
	if _, err := database.SetLibraryUploadState(context.Background(), userID, spaceID, upload.ID, token, "initiated", "uploaded_unverified"); err != nil {
		t.Fatalf("mark %s uploaded = %v", filename, err)
	}
	result, err := database.CompleteLibraryUpload(context.Background(), userID, spaceID, upload.ID, token, 128, digest, mime, nil)
	if err != nil || result.Item == nil {
		t.Fatalf("complete %s = %#v, %v", filename, result, err)
	}
	return result.Item
}
