package db

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// reservedQuota tracks uploads created only to occupy quota so a test can
// release them again.
type reservedQuota struct {
	spaceID  string
	userID   string
	uploadID string
	token    string
}

// reserveQuotaBytes reserves exactly total bytes for one user in one Space.
//
// Per-file limits are now purpose-specific (100 MB for Library), which is far
// below the multi-gigabyte owner storage pool. Quota tests therefore fill the
// pool with several legal uploads instead of one oversized upload.
func reserveQuotaBytes(t *testing.T, database *Database, ctx context.Context, userID, spaceID, keyPrefix string, total int64) []reservedQuota {
	t.Helper()
	if total < 1 {
		return nil
	}
	digest := strings.Repeat("d", 64)
	chunk := DefaultLibraryMaxFileBytes
	reservations := []reservedQuota{}
	for index := 0; total > 0; index++ {
		size := chunk
		if total < size {
			size = total
		}
		token := fmt.Sprintf("%s-token-%d", keyPrefix, index)
		upload, err := database.CreateLibraryUpload(ctx, userID, spaceID, UploadPurposeLibrary,
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

// releaseQuota rejects every reservation so the pool returns to zero.
func releaseQuota(t *testing.T, database *Database, ctx context.Context, reservations []reservedQuota) {
	t.Helper()
	for _, reservation := range reservations {
		if err := database.RejectLibraryUpload(ctx, reservation.userID, reservation.spaceID, reservation.uploadID, reservation.token, "invalid", "test_cleanup"); err != nil {
			t.Fatalf("releasing reservation %s = %v", reservation.uploadID, err)
		}
	}
}
