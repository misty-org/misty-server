package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

const unsentAttachmentGrace = 24 * time.Hour

func (s *SpaceLibraryService) CleanupExpired(ctx context.Context, limit int) (int, error) {
	uploads, err := s.database.ExpireLibraryUploads(ctx, limit)
	if err != nil {
		return 0, err
	}
	for _, upload := range uploads {
		if err := s.Store.Delete(ctx, upload.ObjectKey); err != nil && !errors.Is(err, ErrLibraryObjectNotFound) {
			return len(uploads), err
		}
	}
	if _, err := s.database.DiscardStaleUnsentAttachments(ctx, unsentAttachmentGrace, limit); err != nil {
		return len(uploads), err
	}
	_, err = s.database.ReconcileLibraryStorageUsage(ctx, limit)
	return len(uploads), err
}

func (s *SpaceLibraryService) downloadAttachment(w http.ResponseWriter, r *http.Request, download *db.LibraryDownload, err error) {
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	s.WriteDownload(w, r, download)
}
