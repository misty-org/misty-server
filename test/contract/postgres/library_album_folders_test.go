package db

import (
	"context"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestLibraryAlbumFoldersAndAlbumPresentation(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("Album Folder Owner", "album-folders@example.com", "password123")
	spaceID := createTestSpace(t, database, ctx, owner.ID, "Album folders").ID

	parent, err := database.CreateLibraryAlbumFolder(ctx, owner.ID, spaceID, "", "Trips")
	if err != nil || parent.Name != "Trips" || parent.Version != 1 {
		t.Fatalf("create parent folder = %#v, %v", parent, err)
	}
	child, err := database.CreateLibraryAlbumFolder(ctx, owner.ID, spaceID, parent.ID, "2026")
	if err != nil || child.ParentFolderID != parent.ID {
		t.Fatalf("create child folder = %#v, %v", child, err)
	}
	if _, err := database.UpdateLibraryAlbumFolder(ctx, owner.ID, spaceID, parent.ID, parent.Version, child.ID, parent.Name, parent.Position); err != ErrLibraryInvalid {
		t.Fatalf("folder cycle error = %v", err)
	}

	album, err := database.CreateLibraryAlbum(ctx, owner.ID, spaceID, "Big Sur", "Coast")
	if err != nil || album.ViewMode != "grid" || album.SortMode != "custom" {
		t.Fatalf("create album = %#v, %v", album, err)
	}
	album, err = database.OrganizeLibraryAlbum(ctx, owner.ID, spaceID, album.ID, album.Version, child.ID, "list", "oldest", 4)
	if err != nil || album.FolderID != child.ID || album.ViewMode != "list" || album.SortMode != "oldest" || album.Position != 4 {
		t.Fatalf("organize album = %#v, %v", album, err)
	}
	folders, err := database.LibraryAlbumFolders(ctx, owner.ID, spaceID)
	if err != nil || len(folders) != 2 {
		t.Fatalf("list folders = %#v, %v", folders, err)
	}
	for _, folder := range folders {
		if folder.ID == child.ID && folder.AlbumCount != 1 {
			t.Fatalf("child album count = %#v", folder)
		}
	}
	if err := database.DeleteLibraryAlbumFolder(ctx, owner.ID, spaceID, parent.ID, parent.Version); err != nil {
		t.Fatalf("delete folder tree: %v", err)
	}
	album, err = database.LibraryAlbum(ctx, owner.ID, spaceID, album.ID)
	if err != nil || album.FolderID != "" {
		t.Fatalf("album after folder delete = %#v, %v", album, err)
	}
}

func TestLibraryMediaSubtypeCollections(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("Media Types Owner", "media-types@example.com", "password123")
	spaceID := createTestSpace(t, database, ctx, owner.ID, "Media types").ID
	item := createPeopleTestImage(t, database, owner.ID, spaceID, "front-camera-selfie.jpg", "5")
	if _, err := database.Conn.ExecContext(ctx, `UPDATE library_files SET intrinsic_metadata='{"media_subtypes":["selfie","portrait","screenshot"]}'::jsonb WHERE id=$1`, item.FileID); err != nil {
		t.Fatal(err)
	}
	facets, err := database.LibraryFacets(ctx, owner.ID, spaceID, "")
	if err != nil || libraryFacetCount(facets.MediaTypes, "selfies") != 1 || libraryFacetCount(facets.MediaTypes, "portraits") != 1 || libraryFacetCount(facets.MediaTypes, "screenshots") != 1 {
		t.Fatalf("media subtype facets = %#v, %v", facets, err)
	}
	items, err := database.LibraryItems(ctx, owner.ID, spaceID, LibraryItemQuery{MediaType: "selfies"})
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("selfie collection = %#v, %v", items, err)
	}
}

func TestLibraryDiscoveryUsesIntrinsicCaptureAndLocation(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("Capture Metadata Owner", "capture-metadata@example.com", "password123")
	spaceID := createTestSpace(t, database, ctx, owner.ID, "Capture metadata").ID
	first := createPeopleTestImage(t, database, owner.ID, spaceID, "coast-one.jpg", "3")
	second := createPeopleTestImage(t, database, owner.ID, spaceID, "coast-two.jpg", "4")
	for _, item := range []*SpaceLibraryItem{first, second} {
		if _, err := database.Conn.ExecContext(ctx, `UPDATE library_files SET intrinsic_capture_at='2021-05-06T12:00:00Z',intrinsic_location='{"name":"Big Sur","latitude":36.2704,"longitude":-121.8081}'::jsonb WHERE id=$1`, item.FileID); err != nil {
			t.Fatal(err)
		}
	}
	discovery, err := database.LibraryDiscovery(ctx, owner.ID, spaceID)
	if err != nil || len(discovery.Months) != 1 || discovery.Months[0].ID != "2021-05" || len(discovery.Trips) != 1 || discovery.Trips[0].ID != "Big Sur" {
		t.Fatalf("intrinsic discovery = %#v, %v", discovery, err)
	}
	music := createPeopleTestImage(t, database, owner.ID, spaceID, "soundtrack.mp3", "2")
	if _, err := database.Conn.ExecContext(ctx, `UPDATE library_blobs b SET server_detected_mime_type='audio/mpeg' FROM library_files f WHERE f.blob_id=b.id AND f.id=$1`, music.FileID); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateLibraryMemoryPreference(ctx, owner.ID, spaceID, "2021-05", 0, "Coast in May", first.ID, music.ID, 6); err != nil {
		t.Fatal(err)
	}
	discovery, err = database.LibraryDiscovery(ctx, owner.ID, spaceID)
	if err != nil || len(discovery.Memories) != 1 || discovery.Memories[0].Title != "Coast in May" || discovery.Memories[0].CoverItemID != first.ID || discovery.Memories[0].MusicItemID != music.ID || discovery.Memories[0].PlaybackSeconds != 6 || discovery.Memories[0].PreferenceVersion != 1 {
		t.Fatalf("memory preferences = %#v, %v", discovery, err)
	}
}
