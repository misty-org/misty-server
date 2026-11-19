package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
)

// A deck's images: the same direct-to-R2 flow, image types and size limit as a drawing's. A
// slide's image slot keeps only the asset id.

// spaceDeckInSpace reads the route's deck for a caller who can view it.
func (s *SpaceLibraryService) spaceDeckInSpace(r *http.Request, userID string) (*db.SpaceDeck, error) {
	deck, err := s.database.SpaceMadeItemByID(r.Context(), db.SpaceItemDeck, userID, chi.URLParam(r, "deckID"))
	if err == nil && deck.SpaceID != chi.URLParam(r, "spaceID") {
		err = db.ErrSpaceNotFound
	}
	return deck, err
}

// SpaceDeckAssets lists a deck's images and starts a direct image upload. It never accepts an
// image body; only the presigned R2 URL does.
func (s *SpaceLibraryService) SpaceDeckAssets() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		deck, err := s.spaceDeckInSpace(r, userID)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		if r.Method == http.MethodGet {
			assets, err := s.database.DeckAssets(r.Context(), userID, deck.ID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"assets": assets})
			return
		}
		if !s.DeckAssetsEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "deck_assets_disabled"})
			return
		}
		if !s.DirectTransfersActive() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "journal_asset_direct_transfer_required"})
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
		body.MIMEType = strings.ToLower(strings.TrimSpace(body.MIMEType))
		body.SHA256 = strings.ToLower(strings.TrimSpace(body.SHA256))
		if body.Filename == "" || !SupportedDrawingAssetMIME(body.MIMEType) ||
			body.ByteSize < 1 || body.ByteSize > s.UploadLimits.Max(UploadPurposeDeckAsset) ||
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
		upload, err := s.database.CreateDeckAssetUpload(r.Context(), userID, deck.ID, body.Filename, body.MIMEType,
			body.ByteSize, body.SHA256, objectKey, security.HashToken(token), expiresAt)
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

// SpaceDeckAssetDownload returns a short-lived R2 GET descriptor after rechecking the deck.
func (s *SpaceLibraryService) SpaceDeckAssetDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		deck, err := s.spaceDeckInSpace(r, userID)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		download, err := s.database.DeckAssetDownload(r.Context(), userID, deck.ID, chi.URLParam(r, "assetID"))
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		s.WriteJournalAssetDownload(w, r, download)
	}
}

// SpaceDeckAsset removes a slide's image reference without deleting shared R2 bytes at once.
func (s *SpaceLibraryService) SpaceDeckAsset() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		deck, err := s.spaceDeckInSpace(r, userID)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		if err := s.database.DeleteDeckAsset(r.Context(), userID, deck.ID, chi.URLParam(r, "assetID")); err != nil {
			writeLibraryError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
