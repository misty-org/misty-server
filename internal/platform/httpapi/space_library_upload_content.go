package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/misty-org/misty-server/internal/platform/db"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/security"
)

func (s *SpaceLibraryService) finalizeUpload(w http.ResponseWriter, r *http.Request, userID, spaceID string) {
	uploadID := chi.URLParam(r, "uploadID")
	tokenHash := security.HashToken(strings.TrimSpace(r.Header.Get(LibraryUploadTokenHeader)))
	upload, err := s.database.LibraryUpload(r.Context(), userID, spaceID, uploadID)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	if upload.Purpose == UploadPurposeDrive {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "drive_item_required"})
		return
	}
	if !s.transferPurposeEnabled(upload.Purpose) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_uploads_disabled"})
		return
	}
	if upload.UploadTokenHash != tokenHash {
		writeLibraryError(w, db.ErrLibraryForbidden)
		return
	}
	if err := s.database.ValidateJournalUploadTarget(r.Context(), userID, spaceID, uploadID,
		chi.URLParam(r, "noteID")); err != nil {
		writeLibraryError(w, err)
		return
	}
	if upload.State == "ready" {
		result, err := s.database.CompleteLibraryUpload(r.Context(), userID, spaceID, uploadID, tokenHash, upload.RequestedByteSize, upload.ClientSHA256, upload.DetectedMIMEType, nil)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	directPending := upload.State == "initiated"
	if upload.State != "uploaded_unverified" && !directPending {
		writeLibraryError(w, db.ErrLibraryConflict)
		return
	}
	if directPending {
		if _, err := s.database.SetLibraryUploadState(r.Context(), userID, spaceID, uploadID, tokenHash, "initiated", "uploaded_unverified"); err != nil {
			writeLibraryError(w, err)
			return
		}
		upload.State = "uploaded_unverified"
	}

	metadata, headErr := s.Store.Head(r.Context(), upload.ObjectKey)
	if headErr != nil || metadata.ByteSize != upload.RequestedByteSize || metadata.SHA256 != upload.ClientSHA256 {
		event, _ := json.Marshal(map[string]any{
			"byte_size_match": metadata.ByteSize == upload.RequestedByteSize,
			"checksum_match":  metadata.SHA256 == upload.ClientSHA256,
			"event":           "library_upload_verification_failed",
			"level":           "error",
			"request_id":      r.Header.Get("X-Request-ID"),
			"store_error":     headErr != nil,
		})
		log.Print(string(event))
		s.rejectAndDelete(r.Context(), upload, tokenHash, "invalid", "object_missing_or_mismatched")
		writeLibraryError(w, db.ErrLibraryUploadMismatch)
		return
	}
	if IsJournalAssetPurpose(upload.Purpose) &&
		!SupportedNoteAssetMIME(upload.ClientDeclaredMIMEType) {
		s.rejectAndDelete(
			r.Context(), upload, tokenHash, "invalid", "unsupported_journal_asset_mime",
		)
		writeLibraryError(w, db.ErrLibraryInvalid)
		return
	}
	deduplicationKey, deduplicationErr := s.database.LibraryUploadDeduplicationObjectKey(r.Context(), userID, spaceID, uploadID)
	if deduplicationErr != nil {
		writeLibraryError(w, deduplicationErr)
		return
	}
	if deduplicationKey != "" {
		if _, existingErr := s.Store.Head(r.Context(), deduplicationKey); errors.Is(existingErr, ErrLibraryObjectNotFound) {
			if repairErr := s.database.ReplaceMissingLibraryUploadDeduplicationObject(r.Context(), userID, spaceID, uploadID, deduplicationKey); repairErr != nil {
				writeLibraryError(w, repairErr)
				return
			}
		} else if existingErr != nil {
			writeLibraryError(w, existingErr)
			return
		}
	}
	intrinsic, _ := json.Marshal(map[string]any{
		"byte_size":                 metadata.ByteSize,
		"sha256":                    metadata.SHA256,
		"client_declared_mime_type": upload.ClientDeclaredMIMEType,
	})
	result, err := s.database.CompleteLibraryUpload(r.Context(), userID, spaceID, uploadID, tokenHash, metadata.ByteSize, metadata.SHA256, upload.ClientDeclaredMIMEType, intrinsic)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	if result.DiscardObjectKey != "" {
		_ = s.Store.Delete(r.Context(), result.DiscardObjectKey)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *SpaceLibraryService) abortUpload(w http.ResponseWriter, r *http.Request, userID, spaceID string) {
	uploadID := chi.URLParam(r, "uploadID")
	tokenHash := security.HashToken(strings.TrimSpace(r.Header.Get(LibraryUploadTokenHeader)))
	upload, err := s.database.LibraryUpload(r.Context(), userID, spaceID, uploadID)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	switch upload.State {
	case "rejected", "infected", "invalid", "processing_failed", "expired", "deleted":
		w.WriteHeader(http.StatusNoContent)
		return
	case "ready":
		if err := s.database.DiscardUnsentAttachment(r.Context(), userID, spaceID, uploadID); err != nil {
			writeLibraryError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.database.RejectLibraryUpload(r.Context(), userID, spaceID, uploadID, tokenHash, "rejected", "upload_aborted"); err != nil {
		writeLibraryError(w, err)
		return
	}
	if expected, err := s.database.LibraryObjectExpectations(r.Context(), []string{upload.ObjectKey}); err == nil {
		if expected[upload.ObjectKey].Source != "blob" {
			_ = s.Store.Delete(r.Context(), upload.ObjectKey)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
