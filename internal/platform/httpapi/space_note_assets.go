package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// SpaceNoteAssets lists a note's assets and initiates a new asset upload.
//
// The note_attachment purpose is rejected by the generic Library upload
// endpoint, so this is the only route that can create one, and it authorizes
// against the parent note before reserving any quota.
func (s *SpaceLibraryService) SpaceNoteAssets() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		noteID := chi.URLParam(r, "noteID")
		if r.Method == http.MethodGet {
			assets, err := s.database.NoteAssets(r.Context(), userID, noteID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"assets": assets})
			return
		}
		if !s.NoteAssetsEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "note_assets_disabled"})
			return
		}
		if !s.DirectTransfersActive() {
			writeJSON(w, http.StatusServiceUnavailable,
				map[string]string{"code": "journal_asset_direct_transfer_required"})
			return
		}
		var body struct {
			Filename string `json:"filename"`
			MIMEType string `json:"mime_type"`
			ByteSize int64  `json:"byte_size"`
			SHA256   string `json:"sha256"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		body.Filename = sanitizeLibraryFilename(body.Filename)
		body.SHA256 = strings.ToLower(strings.TrimSpace(body.SHA256))
		body.MIMEType = strings.ToLower(strings.TrimSpace(body.MIMEType))
		maxBytes := s.UploadLimits.Max(UploadPurposeNoteAttachment)
		if maxBytes < 1 || body.ByteSize < 1 || body.ByteSize > maxBytes ||
			!librarySHA256Pattern.MatchString(body.SHA256) || body.Filename == "" ||
			!SupportedDrawingAssetMIME(body.MIMEType) {
			writeLibraryError(w, db.ErrLibraryInvalid)
			return
		}
		token, err := security.GenerateSecureToken()
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		objectKey := "library/" + strings.ReplaceAll(uuid.NewString(), "-", "")
		expiresAt := time.Now().Add(libraryUploadLifetime).UTC()
		upload, err := s.database.CreateNoteAssetUpload(r.Context(), userID, noteID, body.Filename,
			body.MIMEType, body.ByteSize, body.SHA256, objectKey, security.HashToken(token), expiresAt)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		transfer, err := s.UploadTransfer(r.Context(), upload, token, expiresAt)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"upload":   upload,
			"transfer": transfer,
			"finalize": map[string]any{"headers": map[string]string{LibraryUploadTokenHeader: token}},
		})
	}
}

// SpaceNoteAssetDownload issues an authorized download for one asset. Viewers
// may download; only uploading and removal require edit access.
func (s *SpaceLibraryService) SpaceNoteAssetDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		download, err := s.database.NoteAssetDownload(r.Context(), userID,
			chi.URLParam(r, "noteID"), chi.URLParam(r, "assetID"))
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		s.WriteJournalAssetDownload(w, r, download)
	}
}

// SpaceNoteAsset removes one asset reference.
func (s *SpaceLibraryService) SpaceNoteAsset() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.DeleteNoteAsset(r.Context(), userID,
			chi.URLParam(r, "noteID"), chi.URLParam(r, "assetID")); err != nil {
			writeLibraryError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
