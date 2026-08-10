package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// CleanupExpiredJournalAssets removes note/drawing asset references after the
// collaboration undo window. Deduplicated blobs are deleted from R2 only when
// the database atomically confirms that no other live reference remains.
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
			if err := s.TestingStore.Delete(ctx, claim.ObjectKey); err != nil &&
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
				"space_id": usage.SpaceID, "storage_available": usage.RemainingBytes > 0,
			})
			return
		}
		writeJSON(w, http.StatusOK, usage)
	}
}

func (s *SpaceLibraryService) AssetStacks() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			stacks, err := s.database.LibraryAssetStacks(r.Context(), userID, spaceID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"stacks": stacks})
			return
		}
		var input db.CreateLibraryAssetStack
		if decodeJSON(w, r, &input) != nil {
			return
		}
		for _, member := range input.Members {
			if err := s.validateSensitiveLibraryItem(r, userID, spaceID, member.ItemID); err != nil {
				writeLibraryError(w, err)
				return
			}
		}
		stack, err := s.database.CreateLibraryAssetStack(r.Context(), userID, spaceID, input)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, stack)
	}
}

func (s *SpaceLibraryService) AssetStack() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, stackID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "stackID")
		if err := s.validateSensitiveLibraryAssetStack(r, userID, spaceID, stackID); err != nil {
			writeLibraryError(w, err)
			return
		}
		if r.Method == http.MethodPatch {
			var input struct {
				Version     int64  `json:"version"`
				Title       string `json:"title"`
				CoverItemID string `json:"cover_item_id"`
				Effect      string `json:"effect"`
			}
			if decodeJSON(w, r, &input) != nil {
				return
			}
			stack, err := s.database.UpdateLibraryAssetStack(r.Context(), userID, spaceID, stackID, input.Version, input.Title, input.CoverItemID, input.Effect)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, stack)
			return
		}
		version, err := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
		if err != nil {
			writeLibraryError(w, db.ErrLibraryInvalid)
			return
		}
		if err := s.database.DeleteLibraryAssetStack(r.Context(), userID, spaceID, stackID, version); err != nil {
			writeLibraryError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
		if !s.TestingUploadPurposeEnabled(body.Purpose) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_uploads_disabled"})
			return
		}
		body.Filename = sanitizeLibraryFilename(body.Filename)
		body.SHA256 = strings.ToLower(strings.TrimSpace(body.SHA256))
		body.MIMEType = strings.TrimSpace(body.MIMEType)
		maxBytes := s.TestingUploadLimits.Max(body.Purpose)
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
		transfer, err := s.TestingUploadTransfer(r.Context(), upload, token, expiresAt)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"upload":   upload,
			"transfer": transfer,
			"finalize": map[string]any{"headers": map[string]string{TestingLibraryUploadTokenHeader: token}},
		})
	}
}

// uploadTransfer describes how the client should move the bytes. With direct
// transfer it is an absolute presigned R2 PUT that carries no Misty
// credentials; otherwise it is the relative proxy route used in development.
func (s *SpaceLibraryService) TestingUploadTransfer(ctx context.Context, upload *db.LibraryUpload, token string, expiresAt time.Time) (LibraryObjectUpload, error) {
	if s.TestingDirectTransfersActive() {
		metadata := LibraryObjectMetadata{
			ByteSize: upload.RequestedByteSize,
			SHA256:   upload.ClientSHA256,
			MIMEType: upload.ClientDeclaredMIMEType,
		}
		signed, err := s.TestingPresigner.PresignPut(ctx, upload.ObjectKey, metadata, s.TestingTransfers.UploadURLTTL)
		if err != nil {
			return LibraryObjectUpload{}, err
		}
		return LibraryObjectUpload{
			URL: signed.URL, Method: signed.Method, Headers: signed.Headers, ExpiresAt: signed.ExpiresAt,
		}, nil
	}
	return LibraryObjectUpload{
		URL:     fmt.Sprintf("/spaces/%s/library/uploads/%s/content", upload.SpaceID, upload.ID),
		Method:  http.MethodPut,
		Headers: map[string]string{TestingLibraryUploadTokenHeader: token, "Content-Type": upload.ClientDeclaredMIMEType},
		// The proxy route is bounded by the Misty upload reservation, not by a
		// signature, so it keeps the reservation lifetime.
		ExpiresAt: expiresAt,
	}, nil
}
