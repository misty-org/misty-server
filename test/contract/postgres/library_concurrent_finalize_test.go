package db

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestConcurrentLibraryFinalizationIsIdempotent(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser(
		"Concurrent Finalize Owner",
		"concurrent-finalize@example.com",
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	spaceID := createTestSpace(t, database, ctx, owner.ID, "Concurrent finalize").ID
	digest := strings.Repeat("c", 64)
	upload, err := database.CreateLibraryUpload(
		ctx, owner.ID, spaceID, "library", "same.png", "image/png",
		128, digest, "library/concurrent", "concurrent-token",
		time.Now().Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetLibraryUploadState(
		ctx, owner.ID, spaceID, upload.ID, "concurrent-token",
		"initiated", "uploaded_unverified",
	); err != nil {
		t.Fatal(err)
	}

	results := make([]*CompleteLibraryUploadResult, 2)
	errors := make([]error, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errors[index] = database.CompleteLibraryUpload(
				ctx, owner.ID, spaceID, upload.ID, "concurrent-token",
				128, digest, "image/png", nil,
			)
		}(index)
	}
	close(start)
	wait.Wait()

	for index, finalizeErr := range errors {
		if finalizeErr != nil {
			t.Fatalf("finalization %d: %v", index, finalizeErr)
		}
		if results[index] == nil || results[index].Item == nil {
			t.Fatalf("finalization %d result = %#v", index, results[index])
		}
	}
	if results[0].Item.ID != results[1].Item.ID {
		t.Fatalf("concurrent finalization created two items: %s and %s",
			results[0].Item.ID, results[1].Item.ID)
	}
	var items, contributions int
	if err := database.Conn.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM space_library_items WHERE file_id=$1`,
		results[0].File.ID,
	).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if err := database.Conn.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM space_storage_contributions WHERE source_id=$1`,
		results[0].Item.ID,
	).Scan(&contributions); err != nil {
		t.Fatal(err)
	}
	if items != 1 || contributions != 1 {
		t.Fatalf("items=%d contributions=%d, want exactly one of each", items, contributions)
	}
	usage, err := database.SpaceStorageUsage(ctx, owner.ID, spaceID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.ReservedBytes != 0 || usage.UsedBytes != 128 {
		t.Fatalf("storage usage after concurrent finalize = %#v", usage)
	}
}
