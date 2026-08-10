package db

import (
	"testing"
	"time"
)

func TestJournalAssetCleanupPreservesSharedBlobUntilLastReference(t *testing.T) {
	fixture := newNoteFixture(t, "drawing-assets-cleanup")
	drawing, err := fixture.database.CreateSpaceDrawing(
		fixture.ctx, fixture.creator, fixture.spaceID, "Cleanup drawing",
	)
	if err != nil {
		t.Fatal(err)
	}
	const byteSize = int64(4096)
	first := finalizeDrawingAssetForCleanup(
		t, fixture, drawing.ID, "cleanup-file-one", byteSize,
	)
	second := finalizeDrawingAssetForCleanup(
		t, fixture, drawing.ID, "cleanup-file-two", byteSize,
	)
	if first.DrawingAsset == nil || second.DrawingAsset == nil {
		t.Fatalf("missing finalized assets: first=%#v second=%#v", first, second)
	}
	if first.File.BlobID != second.File.BlobID {
		t.Fatalf(
			"deduplicated assets used different blobs: %s != %s",
			first.File.BlobID, second.File.BlobID,
		)
	}

	if err := fixture.database.DeleteDrawingAsset(
		fixture.ctx, fixture.creator, drawing.ID, first.DrawingAsset.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Conn.ExecContext(
		fixture.ctx,
		`UPDATE space_drawing_assets SET deleted_at=NOW()-INTERVAL '25 hours'
		 WHERE id=$1`,
		first.DrawingAsset.ID,
	); err != nil {
		t.Fatal(err)
	}
	claims, err := fixture.database.ClaimExpiredJournalAssets(
		fixture.ctx, 24*time.Hour, 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].DeleteBlob {
		t.Fatalf("first cleanup claims = %#v, want one shared-blob claim", claims)
	}
	if err := fixture.database.CompleteJournalAssetPurge(
		fixture.ctx, claims[0],
	); err != nil {
		t.Fatal(err)
	}
	var firstState, blobState string
	if err := fixture.database.Conn.QueryRowContext(
		fixture.ctx,
		`SELECT a.lifecycle_state,b.lifecycle_state
		 FROM space_drawing_assets a
		 JOIN library_files f ON f.id=a.file_id
		 JOIN library_blobs b ON b.id=f.blob_id
		 WHERE a.id=$1`,
		first.DrawingAsset.ID,
	).Scan(&firstState, &blobState); err != nil {
		t.Fatal(err)
	}
	if firstState != "deleted" || blobState != "ready" {
		t.Fatalf("first asset state=%q blob state=%q", firstState, blobState)
	}

	if err := fixture.database.DeleteDrawingAsset(
		fixture.ctx, fixture.creator, drawing.ID, second.DrawingAsset.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Conn.ExecContext(
		fixture.ctx,
		`UPDATE space_drawing_assets SET deleted_at=NOW()-INTERVAL '25 hours'
		 WHERE id=$1`,
		second.DrawingAsset.ID,
	); err != nil {
		t.Fatal(err)
	}
	claims, err = fixture.database.ClaimExpiredJournalAssets(
		fixture.ctx, 24*time.Hour, 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || !claims[0].DeleteBlob ||
		claims[0].ObjectKey == "" {
		t.Fatalf("last cleanup claims = %#v, want R2 object deletion", claims)
	}
	if err := fixture.database.CompleteJournalAssetPurge(
		fixture.ctx, claims[0],
	); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.Conn.QueryRowContext(
		fixture.ctx,
		`SELECT lifecycle_state FROM library_blobs WHERE id=$1`,
		first.File.BlobID,
	).Scan(&blobState); err != nil {
		t.Fatal(err)
	}
	if blobState != "deleted" {
		t.Fatalf("last-reference blob state = %q, want deleted", blobState)
	}
}
