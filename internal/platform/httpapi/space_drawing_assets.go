package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

var drawingAssetMIMETypes = map[string]bool{
	"image/avif":               true,
	"image/bmp":                true,
	"image/gif":                true,
	"image/jpeg":               true,
	"image/png":                true,
	"image/webp":               true,
	"image/x-icon":             true,
	"image/vnd.microsoft.icon": true,
}

func TestingSupportedDrawingAssetMIME(value string) bool {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	return drawingAssetMIMETypes[value]
}

// SpaceDrawingAssets lists R2 references and initiates direct image uploads.
// It never accepts an image request body; only the presigned R2 URL does.
func (s *SpaceLibraryService) SpaceDrawingAssets() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, drawingID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "drawingID")
		drawing, err := s.database.SpaceDrawingByID(r.Context(), userID, drawingID)
		if err != nil || drawing.SpaceID != spaceID {
			if err == nil {
				err = db.ErrSpaceNotFound
			}
			writeLibraryError(w, err)
			return
		}
		if r.Method == http.MethodGet {
			assets, err := s.database.DrawingAssets(r.Context(), userID, drawingID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"assets": assets})
			return
		}
		if !s.TestingDrawingAssetsEnabled {
			writeJSON(
				w,
				http.StatusServiceUnavailable,
				map[string]string{"code": "drawing_assets_disabled"},
			)
			return
		}
		if !s.TestingDirectTransfersActive() {
			writeJSON(
				w,
				http.StatusServiceUnavailable,
				map[string]string{"code": "journal_asset_direct_transfer_required"},
			)
			return
		}
		var body struct {
			FileID   string `json:"file_id"`
			Filename string `json:"filename"`
			MIMEType string `json:"mime_type"`
			ByteSize int64  `json:"byte_size"`
			SHA256   string `json:"sha256"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		body.FileID = strings.TrimSpace(body.FileID)
		body.Filename = sanitizeLibraryFilename(body.Filename)
		body.MIMEType = strings.ToLower(strings.TrimSpace(body.MIMEType))
		body.SHA256 = strings.ToLower(strings.TrimSpace(body.SHA256))
		maxBytes := s.TestingUploadLimits.Max(UploadPurposeDrawingAsset)
		if body.FileID == "" || len(body.FileID) > 160 ||
			body.Filename == "" || !TestingSupportedDrawingAssetMIME(body.MIMEType) ||
			body.ByteSize < 1 || body.ByteSize > maxBytes ||
			!librarySHA256Pattern.MatchString(body.SHA256) {
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
		upload, err := s.database.CreateDrawingAssetUpload(
			r.Context(),
			userID,
			drawingID,
			body.FileID,
			body.Filename,
			body.MIMEType,
			body.ByteSize,
			body.SHA256,
			objectKey,
			security.HashToken(token),
			expiresAt,
		)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		transfer, err := s.TestingUploadTransfer(r.Context(), upload, token, expiresAt)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"upload":   upload,
			"transfer": transfer,
			"finalize": map[string]any{
				"headers": map[string]string{TestingLibraryUploadTokenHeader: token},
			},
		})
	}
}

// SpaceDrawingAssetDownload returns a short-lived R2 GET descriptor after
// rechecking current drawing membership.
func (s *SpaceLibraryService) SpaceDrawingAssetDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		drawingID := chi.URLParam(r, "drawingID")
		drawing, err := s.database.SpaceDrawingByID(r.Context(), userID, drawingID)
		if err != nil || drawing.SpaceID != chi.URLParam(r, "spaceID") {
			if err == nil {
				err = db.ErrSpaceNotFound
			}
			writeLibraryError(w, err)
			return
		}
		download, err := s.database.DrawingAssetDownload(
			r.Context(),
			userID,
			drawingID,
			chi.URLParam(r, "assetID"),
		)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		s.TestingWriteJournalAssetDownload(w, r, download)
	}
}

// SpaceDrawingAsset removes the scene's stable reference without synchronously
// deleting shared R2 bytes.
func (s *SpaceLibraryService) SpaceDrawingAsset() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		drawingID := chi.URLParam(r, "drawingID")
		drawing, err := s.database.SpaceDrawingByID(r.Context(), userID, drawingID)
		if err != nil || drawing.SpaceID != chi.URLParam(r, "spaceID") {
			if err == nil {
				err = db.ErrSpaceNotFound
			}
			writeLibraryError(w, err)
			return
		}
		if err := s.database.DeleteDrawingAsset(
			r.Context(),
			userID,
			drawingID,
			chi.URLParam(r, "assetID"),
		); err != nil {
			writeLibraryError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
