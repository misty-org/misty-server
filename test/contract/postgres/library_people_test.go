package db

import (
	"context"
	"strings"
	"testing"
	"time"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)


import ()

func createPeopleTestImage(t *testing.T, database *Database, userID, spaceID, filename, digestCharacter string) *SpaceLibraryItem {
	t.Helper()
	digest := strings.Repeat(digestCharacter, 64)
	token := "people-token-" + digestCharacter
	upload, err := database.CreateLibraryUpload(context.Background(), userID, spaceID, "library", filename, "image/jpeg", 128, digest, "library/people-"+digestCharacter, token, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetLibraryUploadState(context.Background(), userID, spaceID, upload.ID, token, "initiated", "uploaded_unverified"); err != nil {
		t.Fatal(err)
	}
	result, err := database.CompleteLibraryUpload(context.Background(), userID, spaceID, upload.ID, token, 128, digest, "image/jpeg", nil)
	if err != nil || result.Item == nil {
		t.Fatalf("complete image = %#v, %v", result, err)
	}
	return result.Item
}
