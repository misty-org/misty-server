package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/misty-org/misty-server/internal/platform/security"
)

func (s *SpaceLibraryService) CleanupExpiredJournalAssets(
	ctx context.Context,
	safetyWindow time.Duration,
	limit int,
) (int, error) {
	claims, err := s.database.ClaimExpiredJournalAssets(ctx, safetyWindow, limit)
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, claim := range claims {
		if claim.DeleteBlob {
			if err := s.Store.Delete(ctx, claim.ObjectKey); err != nil &&
				!errors.Is(err, ErrLibraryObjectNotFound) {
				return completed, err
			}
		}
		if err := s.database.CompleteJournalAssetPurge(ctx, claim); err != nil {
			return completed, err
		}
		completed++
	}
	_, err = s.database.ReconcileLibraryStorageUsage(ctx, limit)
	return completed, err
}

func (s *SpaceLibraryService) Usage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		usage, err := s.database.SpaceStorageUsage(r.Context(), userID, chi.URLParam(r, "spaceID"))
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		if usage.OwnerUserID != userID {
			writeJSON(w, http.StatusOK, map[string]any{
				"space_id": usage.SpaceID, "storage_available": true,
			})
			return
		}
		writeJSON(w, http.StatusOK, usage)
	}
}

func (s *SpaceLibraryService) InitiateUpload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Filename       string `json:"filename"`
			MIMEType       string `json:"mime_type"`
			ByteSize       int64  `json:"byte_size"`
			SHA256         string `json:"sha256"`
			Purpose        string `json:"purpose"`
			ConversationID string `json:"conversation_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if !s.UploadPurposeEnabled(body.Purpose) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_uploads_disabled"})
			return
		}
		body.Filename = sanitizeLibraryFilename(body.Filename)
		body.SHA256 = strings.ToLower(strings.TrimSpace(body.SHA256))
		body.MIMEType = strings.TrimSpace(body.MIMEType)
		maxBytes := s.UploadLimits.Max(body.Purpose)
		if maxBytes < 1 || body.ByteSize < 1 || body.ByteSize > maxBytes || !librarySHA256Pattern.MatchString(body.SHA256) || body.Filename == "" {
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
		upload, err := s.database.CreateLibraryUploadForConversation(r.Context(), userID, chi.URLParam(r, "spaceID"), body.ConversationID, body.Purpose, body.Filename, body.MIMEType, body.ByteSize, body.SHA256, objectKey, security.HashToken(token), expiresAt)
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

func (s *SpaceLibraryService) UploadTransfer(ctx context.Context, upload *db.LibraryUpload, token string, expiresAt time.Time) (LibraryObjectUpload, error) {
	_, _ = token, expiresAt
	if s.DirectTransfersActive() {
		metadata := LibraryObjectMetadata{
			ByteSize: upload.RequestedByteSize,
			SHA256:   upload.ClientSHA256,
			MIMEType: upload.ClientDeclaredMIMEType,
		}
		signed, err := s.Presigner.PresignPut(ctx, upload.ObjectKey, metadata, s.Transfers.UploadURLTTL)
		if err != nil {
			return LibraryObjectUpload{}, err
		}
		return LibraryObjectUpload{
			URL: signed.URL, Method: signed.Method, Headers: signed.Headers, ExpiresAt: signed.ExpiresAt,
		}, nil
	}
	return LibraryObjectUpload{}, ErrLibraryDirectTransferUnavailable
}
