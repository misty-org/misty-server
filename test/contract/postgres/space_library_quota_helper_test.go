package db

import (
	"context"
	"fmt"
	. "github.com/misty-org/misty-server/internal/platform/db"
	"strings"
	"testing"
	"time"
)

type reservedQuota struct {
	spaceID  string
	userID   string
	uploadID string
	token    string
}

func reserveQuotaBytes(t *testing.T, database *Database, ctx context.Context, userID, spaceID, keyPrefix string, total int64) []reservedQuota {
	t.Helper()
	if total < 1 {
		return nil
	}
	digest := strings.Repeat("d", 64)
	chunk := DefaultChatAttachmentMaxFileBytes
	reservations := []reservedQuota{}
	for index := 0; total > 0; index++ {
		size := chunk
		if total < size {
			size = total
		}
		token := fmt.Sprintf("%s-token-%d", keyPrefix, index)
		upload, err := database.CreateLibraryUpload(ctx, userID, spaceID, UploadPurposeChatAttachment,
			fmt.Sprintf("%s-%d.bin", keyPrefix, index), "application/octet-stream", size, digest,
			fmt.Sprintf("library/%s-%d", keyPrefix, index), token, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatalf("reserving %d bytes (chunk %d) = %v", size, index, err)
		}
		reservations = append(reservations, reservedQuota{spaceID: spaceID, userID: userID, uploadID: upload.ID, token: token})
		total -= size
	}
	return reservations
}

func releaseQuota(t *testing.T, database *Database, ctx context.Context, reservations []reservedQuota) {
	t.Helper()
	for _, reservation := range reservations {
		if err := database.RejectLibraryUpload(ctx, reservation.userID, reservation.spaceID, reservation.uploadID, reservation.token, "invalid", "test_cleanup"); err != nil {
			t.Fatalf("releasing reservation %s = %v", reservation.uploadID, err)
		}
	}
}
