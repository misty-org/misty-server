package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// CleanupExpired gives up on uploads whose reservation ran out and deletes
// whatever bytes they left in the object store.
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
	_, err = s.database.ReconcileLibraryStorageUsage(ctx, limit)
	return len(uploads), err
}

// DownloadAttachment signs a download of a file posted in a chat, for someone
// who can read the message it was posted with.
func (s *SpaceLibraryService) DownloadAttachment() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		download, err := s.database.MessageAttachmentDownload(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "attachmentID"))
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		s.WriteDownload(w, r, download)
	}
}
