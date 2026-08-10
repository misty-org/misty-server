package api

import (
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
)

func (s *SpaceLibraryService) MergePeople() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.peopleEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_people_disabled"})
			return
		}
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			SourceID      string `json:"source_id"`
			TargetID      string `json:"target_id"`
			SourceVersion int64  `json:"source_version"`
			TargetVersion int64  `json:"target_version"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		person, err := s.database.MergeLibraryPeople(r.Context(), userID, chi.URLParam(r, "spaceID"), body.SourceID, body.TargetID, body.SourceVersion, body.TargetVersion)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, person)
	}
}

func (s *SpaceLibraryService) EditVersions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.editingEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_editing_disabled"})
			return
		}
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, itemID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "itemID")
		if err := s.validateSensitiveLibraryItem(r, userID, spaceID, itemID); err != nil {
			writeLibraryError(w, err)
			return
		}
		switch r.Method {
		case http.MethodGet:
			versions, err := s.database.LibraryEditVersions(r.Context(), userID, spaceID, itemID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
		case http.MethodPost:
			var body struct {
				ItemVersion    int64                    `json:"item_version"`
				EditDefinition db.LibraryEditDefinition `json:"edit_definition"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			result, err := s.database.CreateLibraryEditVersion(r.Context(), userID, spaceID, itemID, body.ItemVersion, body.EditDefinition)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, result)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpaceLibraryService) SelectEditVersion() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.editingEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_editing_disabled"})
			return
		}
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, itemID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "itemID")
		if err := s.validateSensitiveLibraryItem(r, userID, spaceID, itemID); err != nil {
			writeLibraryError(w, err)
			return
		}
		var body struct {
			ItemVersion int64  `json:"item_version"`
			EditID      string `json:"edit_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		result, err := s.database.SelectLibraryEditVersion(r.Context(), userID, spaceID, itemID, body.EditID, body.ItemVersion)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func (s *SpaceLibraryService) DeleteEditVersion() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.editingEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_editing_disabled"})
			return
		}
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, itemID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "itemID")
		if err := s.validateSensitiveLibraryItem(r, userID, spaceID, itemID); err != nil {
			writeLibraryError(w, err)
			return
		}
		if err := s.database.DeleteLibraryEditVersion(r.Context(), userID, spaceID, itemID, chi.URLParam(r, "editID")); err != nil {
			writeLibraryError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpaceLibraryService) TestingWriteDownload(w http.ResponseWriter, r *http.Request, download *db.LibraryDownload) {
	// Bandwidth is billed per byte by object storage and by the host, so the
	// ceiling is checked before anything is streamed.
	if s.egress != nil && !s.egress.Allow(TestingRateLimitIdentity(r), download.ByteSize) {
		WriteQuotaExceeded(w)
		return
	}
	filename := download.Filename
	if download.Rendition {
		filename = libraryRenditionFilename(filename, download.MIMEType)
	}
	// Authorization already succeeded above. With direct transfer the VPS hands
	// back a short-lived signed URL instead of streaming the bytes itself.
	if s.TestingDirectTransfersActive() {
		descriptor, err := s.TestingPresigner.PresignGet(r.Context(), download.ObjectKey, filename, s.TestingTransfers.DownloadURLTTL)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		// An explicit marker, rather than the content type, tells the client this
		// is a descriptor. A user's own uploaded .json file would otherwise be
		// indistinguishable from a descriptor on the proxy path.
		w.Header().Set(TestingLibrarySignedDownloadHeader, "1")
		writeJSON(w, http.StatusOK, descriptor)
		return
	}
	reader, metadata, err := s.TestingStore.Open(r.Context(), download.ObjectKey)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	defer reader.Close()
	if metadata.ByteSize != download.ByteSize || metadata.SHA256 != download.SHA256 {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "library_object_mismatch"})
		return
	}
	w.Header().Set("Content-Type", download.MIMEType)
	w.Header().Set("Content-Length", strconv.FormatInt(download.ByteSize, 10))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": sanitizeLibraryFilename(filename)}))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

// writeJournalAssetDownload always returns a signed R2 descriptor. Journal
// image bodies are never opened or proxied by the API, including in local
// development.
func (s *SpaceLibraryService) TestingWriteJournalAssetDownload(
	w http.ResponseWriter,
	r *http.Request,
	download *db.LibraryDownload,
) {
	if !s.TestingDirectTransfersActive() {
		writeJSON(
			w,
			http.StatusServiceUnavailable,
			map[string]string{"code": "journal_asset_direct_transfer_required"},
		)
		return
	}
	if s.egress != nil && !s.egress.Allow(TestingRateLimitIdentity(r), download.ByteSize) {
		WriteQuotaExceeded(w)
		return
	}
	descriptor, err := s.TestingPresigner.PresignGet(
		r.Context(),
		download.ObjectKey,
		download.Filename,
		s.TestingTransfers.DownloadURLTTL,
	)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	descriptor.MIMEType = download.MIMEType
	descriptor.ByteSize = download.ByteSize
	descriptor.SHA256 = download.SHA256
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set(TestingLibrarySignedDownloadHeader, "1")
	writeJSON(w, http.StatusOK, descriptor)
}

func libraryRenditionFilename(filename, mimeType string) string {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	if strings.TrimSpace(base) == "" {
		base = "edited"
	} else {
		base += "-edited"
	}
	extension := ".bin"
	switch strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0])) {
	case "image/jpeg":
		extension = ".jpg"
	case "video/mp4":
		extension = ".mp4"
	}
	return base + extension
}
