package db

import (
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func createDrawingAssetUpload(
	t *testing.T,
	fixture noteFixture,
	userID, drawingID, fileID string,
	byteSize int64,
) (*LibraryUpload, error) {
	t.Helper()
	return fixture.database.CreateDrawingAssetUpload(
		fixture.ctx,
		userID,
		drawingID,
		fileID,
		fileID+".png",
		"image/png",
		byteSize,
		strings.Repeat("a", 64),
		"library/drawingasset"+fileID,
		"drawing-token-"+fileID,
		time.Now().Add(time.Hour),
	)
}

func TestDrawingAssetUploadAuthorizationAndBinding(t *testing.T) {
	fixture := newNoteFixture(t, "drawing-assets-auth")
	drawing, err := fixture.database.CreateSpaceDrawing(
		fixture.ctx,
		fixture.creator,
		fixture.spaceID,
		"Asset drawing",
	)
	if err != nil {
		t.Fatal(err)
	}

	upload, err := createDrawingAssetUpload(
		t, fixture, fixture.member, drawing.ID, "filemember", 1024,
	)
	if err != nil {
		t.Fatal(err)
	}
	var drawingID, drawingFileID, purpose string
	if err := fixture.database.Conn.QueryRow(
		`SELECT COALESCE(drawing_id,''),COALESCE(drawing_file_id,''),purpose
		 FROM space_library_uploads WHERE id=$1`,
		upload.ID,
	).Scan(&drawingID, &drawingFileID, &purpose); err != nil {
		t.Fatal(err)
	}
	if drawingID != drawing.ID ||
		drawingFileID != "filemember" ||
		purpose != UploadPurposeDrawingAsset {
		t.Fatalf(
			"binding = drawing:%q file:%q purpose:%q",
			drawingID, drawingFileID, purpose,
		)
	}

	outsider, err := fixture.database.CreateUser(
		"Drawing Asset Outsider",
		"drawing-assets-outsider@example.com",
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := createDrawingAssetUpload(
		t, fixture, outsider.ID, drawing.ID, "fileoutsider", 1024,
	); !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("outsider upload error = %v, want ErrLibraryNotFound", err)
	}
	if _, err := createDrawingAssetUpload(
		t, fixture, fixture.creator, drawing.ID, "filelarge",
		DefaultDrawingAssetMaxFileBytes+1,
	); !errors.Is(err, ErrLibraryInvalid) {
		t.Fatalf("oversized upload error = %v, want ErrLibraryInvalid", err)
	}
}

func TestDrawingAssetFinalizationCreatesAuthorizedReference(t *testing.T) {
	fixture := newNoteFixture(t, "drawing-assets-finalize")
	drawing, err := fixture.database.CreateSpaceDrawing(
		fixture.ctx,
		fixture.creator,
		fixture.spaceID,
		"Finalized asset drawing",
	)
	if err != nil {
		t.Fatal(err)
	}
	const (
		excalidrawFileID = "filefinalized"
		tokenHash        = "drawing-token-filefinalized"
		byteSize         = int64(2048)
	)
	upload, err := createDrawingAssetUpload(
		t, fixture, fixture.creator, drawing.ID, excalidrawFileID, byteSize,
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
	if completed.DrawingAsset == nil ||
		completed.DrawingAsset.DrawingID != drawing.ID ||
		completed.DrawingAsset.ExcalidrawFileID != excalidrawFileID {
		t.Fatalf("completed drawing asset = %#v", completed.DrawingAsset)
	}
	var scanStatus string
	if err := fixture.database.Conn.QueryRow(
		`SELECT scan_status FROM library_blobs WHERE id=$1`,
		completed.File.BlobID,
	).Scan(&scanStatus); err != nil {
		t.Fatal(err)
	}
	if scanStatus != "skipped" {
		t.Fatalf("drawing asset scan status = %q, want skipped", scanStatus)
	}

	assets, err := fixture.database.DrawingAssets(
		fixture.ctx,
		fixture.member,
		drawing.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 ||
		assets[0].ID != completed.DrawingAsset.ID ||
		assets[0].SHA256 != strings.Repeat("a", 64) {
		t.Fatalf("assets = %#v", assets)
	}
	download, err := fixture.database.DrawingAssetDownload(
		fixture.ctx,
		fixture.member,
		drawing.ID,
		completed.DrawingAsset.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if download.ByteSize != byteSize || download.MIMEType != "image/png" {
		t.Fatalf("download = %#v", download)
	}
}

func finalizeDrawingAssetForCleanup(
	t *testing.T,
	fixture noteFixture,
	drawingID, excalidrawFileID string,
	byteSize int64,
) *CompleteLibraryUploadResult {
	t.Helper()
	tokenHash := "drawing-token-" + excalidrawFileID
	upload, err := createDrawingAssetUpload(
		t, fixture, fixture.creator, drawingID, excalidrawFileID, byteSize,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.SetLibraryUploadState(
		fixture.ctx, fixture.creator, fixture.spaceID, upload.ID, tokenHash,
		"initiated", "uploaded_unverified",
	); err != nil {
		t.Fatal(err)
	}
	completed, err := fixture.database.CompleteLibraryUpload(
		fixture.ctx, fixture.creator, fixture.spaceID, upload.ID, tokenHash,
		byteSize, strings.Repeat("a", 64), "image/png", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	return completed
}
