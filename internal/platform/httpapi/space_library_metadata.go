package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/misty-org/misty-server/internal/platform/db"
)

func (s *SpaceLibraryService) rejectAndDelete(ctx context.Context, upload *db.LibraryUpload, tokenHash, state, code string) {
	_ = s.database.RejectLibraryUpload(ctx, upload.UserID, upload.SpaceID, upload.ID, tokenHash, state, code)
	_ = s.Store.Delete(ctx, upload.ObjectKey)
}

func sanitizeLibraryFilename(value string) string {
	value = strings.TrimSpace(filepath.Base(strings.ReplaceAll(value, "\\", "/")))
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, value)
	if len([]rune(value)) > 255 {
		value = string([]rune(value)[:255])
	}
	return value
}

func writeLibraryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrLibraryNotFound), errors.Is(err, ErrLibraryObjectNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
	case errors.Is(err, db.ErrLibraryForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "forbidden"})
	case errors.Is(err, db.ErrLibraryInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
	case errors.Is(err, db.ErrLibraryQuota):
		writeJSON(w, http.StatusConflict, map[string]any{"code": "owner_storage_quota_exceeded"})
	case errors.Is(err, db.ErrLibraryConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "version_conflict"})
	case errors.Is(err, db.ErrLibraryUploadMismatch):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "upload_verification_failed"})
	case errors.Is(err, ErrLibraryDirectTransferUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_direct_transfer_unavailable"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
	}
}
