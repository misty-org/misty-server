package db

import (
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestLibraryObjectReconciliationTracksInterruptedAndReadyObjects(t *testing.T) {
	fixture := newNoteFixture(t, "object-reconciliation")
	drawing, err := fixture.database.CreateSpaceDrawing(
		fixture.ctx, fixture.creator, fixture.spaceID, "Reconciliation drawing",
	)
	if err != nil {
		t.Fatal(err)
	}
	const (
		fileID    = "reconciliationfile"
		objectKey = "library/reconciliationobject"
		byteSize  = int64(4096)
		tokenHash = "reconciliation-token"
	)
	upload, err := fixture.database.CreateDrawingAssetUpload(
		fixture.ctx,
		fixture.creator,
		drawing.ID,
		fileID,
		"reconciliation.png",
		"image/png",
		byteSize,
		strings.Repeat("a", 64),
		objectKey,
		tokenHash,
		time.Now().Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.SetLibraryUploadState(
		fixture.ctx,
		fixture.creator,
		fixture.spaceID,
		upload.ID,
		tokenHash,
		"initiated",
		"uploaded_unverified",
	); err != nil {
		t.Fatal(err)
	}

	pending, err := fixture.database.LibraryInterruptedFinalizations(fixture.ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !containsObjectExpectation(pending, objectKey, "upload") {
		t.Fatalf("interrupted finalizations = %#v", pending)
	}
	expected, err := fixture.database.LibraryObjectExpectations(
		fixture.ctx, []string{objectKey, "library/notpresent"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if expected[objectKey].Source != "upload" || expected[objectKey].ByteSize != byteSize {
		t.Fatalf("pending expectation = %#v", expected[objectKey])
	}
	if _, ok := expected["library/notpresent"]; ok {
		t.Fatal("unknown object was treated as referenced")
	}

	completed, err := fixture.database.CompleteLibraryUpload(
		fixture.ctx,
		fixture.creator,
		fixture.spaceID,
		upload.ID,
		tokenHash,
		byteSize,
		strings.Repeat("a", 64),
		"image/png",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if completed.DrawingAsset == nil {
		t.Fatalf("completed upload = %#v", completed)
	}
	expected, err = fixture.database.LibraryObjectExpectations(
		fixture.ctx, []string{objectKey},
	)
	if err != nil {
		t.Fatal(err)
	}
	if expected[objectKey].Source != "blob" {
		t.Fatalf("ready expectation = %#v", expected[objectKey])
	}
}

func containsObjectExpectation(
	items []LibraryObjectExpectation, key, source string,
) bool {
	for _, item := range items {
		if item.ObjectKey == key && item.Source == source {
			return true
		}
	}
	return false
}
