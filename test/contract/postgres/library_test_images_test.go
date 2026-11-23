package db

import (
	"context"
	. "github.com/misty-org/misty-server/internal/platform/db"
	"strings"
	"testing"
	"time"
)

func createTestImage(t *testing.T, database *Database, userID, spaceID, filename, digestCharacter string) *MessageAttachment {
	t.Helper()
	digest := strings.Repeat(digestCharacter, 64)
	token := "image-token-" + digestCharacter
	upload, err := database.CreateLibraryUpload(context.Background(), userID, spaceID, UploadPurposeChatAttachment, filename, "image/jpeg", 128, digest, "library/image-"+digestCharacter, token, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetLibraryUploadState(context.Background(), userID, spaceID, upload.ID, token, "initiated", "uploaded_unverified"); err != nil {
		t.Fatal(err)
	}
	result, err := database.CompleteLibraryUpload(context.Background(), userID, spaceID, upload.ID, token, 128, digest, "image/jpeg", nil)
	if err != nil || result.Attachment == nil {
		t.Fatalf("complete image = %#v, %v", result, err)
	}
	return result.Attachment
}
