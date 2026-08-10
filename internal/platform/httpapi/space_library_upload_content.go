package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func (s *SpaceLibraryService) UploadContent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, uploadID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "uploadID")
		tokenHash := security.HashToken(strings.TrimSpace(r.Header.Get(TestingLibraryUploadTokenHeader)))
		pending, err := s.database.LibraryUpload(r.Context(), userID, spaceID, uploadID)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		if !s.transferPurposeEnabled(pending.Purpose) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_uploads_disabled"})
			return
		}
		// The proxy route exists only for the local development object store.
		// Once direct transfer is on, large user bodies must never reach the VPS.
		if s.TestingDirectTransfersActive() || TestingIsJournalAssetPurpose(pending.Purpose) {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "library_direct_transfer_required"})
			return
		}
		upload, err := s.database.SetLibraryUploadState(r.Context(), userID, spaceID, uploadID, tokenHash, "initiated", "uploading")
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		if r.ContentLength != upload.RequestedByteSize {
			s.rejectAndDelete(r.Context(), upload, tokenHash, "invalid", "content_length_mismatch")
			writeLibraryError(w, db.ErrLibraryUploadMismatch)
			return
		}
		metadata := LibraryObjectMetadata{ByteSize: upload.RequestedByteSize, SHA256: upload.ClientSHA256, MIMEType: upload.ClientDeclaredMIMEType}
		if err := s.TestingStore.Put(r.Context(), upload.ObjectKey, io.LimitReader(r.Body, upload.RequestedByteSize+1), metadata); err != nil {
			s.rejectAndDelete(r.Context(), upload, tokenHash, "invalid", "object_write_failed")
			writeLibraryError(w, err)
			return
		}
		upload, err = s.database.SetLibraryUploadState(r.Context(), userID, spaceID, uploadID, tokenHash, "uploading", "uploaded_unverified")
		if err != nil {
			_ = s.TestingStore.Delete(r.Context(), upload.ObjectKey)
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, upload)
	}
}

func (s *SpaceLibraryService) FinalizeUpload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, uploadID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "uploadID")
		tokenHash := security.HashToken(strings.TrimSpace(r.Header.Get(TestingLibraryUploadTokenHeader)))
		upload, err := s.database.LibraryUpload(r.Context(), userID, spaceID, uploadID)
		if err != nil {
			writeLibraryError(w, err)
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
		if upload.State == "ready" {
			result, err := s.database.CompleteLibraryUpload(r.Context(), userID, spaceID, uploadID, tokenHash, upload.RequestedByteSize, upload.ClientSHA256, upload.DetectedMIMEType, nil)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, result)
			return
		}
		// With direct transfer the client PUTs straight to R2, so the upload is
		// still "initiated" here: the HEAD below is the only proof the bytes
		// landed. The proxy path advances to "uploaded_unverified" first.
		directPending := s.TestingDirectTransfersActive() && upload.State == "initiated"
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

		// Finalization verifies that the object actually in R2 matches exactly
		// what the server authorized: same key, size, and checksum.
		metadata, headErr := s.TestingStore.Head(r.Context(), upload.ObjectKey)
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
		// Journal assets are intentionally never streamed through this process,
		// so they cannot be content-sniffed or synchronously scanned here.
		// Restrict them to passive raster formats at both initiation and
		// finalization; the database records the resulting blob as "skipped",
		// never falsely as malware-scanned clean.
		if TestingIsJournalAssetPurpose(upload.Purpose) &&
			!TestingSupportedDrawingAssetMIME(upload.ClientDeclaredMIMEType) {
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
			if _, existingErr := s.TestingStore.Head(r.Context(), deduplicationKey); errors.Is(existingErr, ErrLibraryObjectNotFound) {
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
			_ = s.TestingStore.Delete(r.Context(), result.DiscardObjectKey)
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func (s *SpaceLibraryService) DownloadItem() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, itemID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "itemID")
		if err := s.validateSensitiveLibraryItem(r, userID, spaceID, itemID); err != nil {
			writeLibraryError(w, err)
			return
		}
		var download *db.LibraryDownload
		var err error
		if r.URL.Query().Get("version") == "original" {
			download, err = s.database.LibraryOriginalItemDownload(r.Context(), userID, spaceID, itemID)
		} else {
			download, err = s.database.LibraryItemDownload(r.Context(), userID, spaceID, itemID)
		}
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		s.TestingWriteDownload(w, r, download)
	}
}
