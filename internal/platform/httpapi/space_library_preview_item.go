package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func (s *SpaceLibraryService) PreviewItem() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.previewsEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_previews_disabled"})
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
		original := r.URL.Query().Get("version") == "original"
		source, err := s.database.LibraryItemPreviewSource(r.Context(), userID, spaceID, itemID, original)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		if source.PreviewObjectKey == "" {
			// Completion consumes the reservation. Every earlier exit releases it.
			defer func() { _ = s.database.ReleaseLibraryPreviewReservation(context.Background(), userID, spaceID, itemID) }()
		}
		if source.PreviewObjectKey == "" && s.mediaProcessor == nil {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"code": "preview_unavailable"})
			return
		}
		if source.PreviewObjectKey == "" {
			reader, metadata, openErr := s.TestingStore.Open(r.Context(), source.ObjectKey)
			if openErr != nil {
				writeLibraryError(w, openErr)
				return
			}
			if metadata.ByteSize != source.ByteSize || metadata.SHA256 != source.SHA256 {
				_ = reader.Close()
				writeJSON(w, http.StatusConflict, map[string]string{"code": "library_object_mismatch"})
				return
			}
			rendered, renderErr := s.mediaProcessor.Preview(r.Context(), reader, source.ByteSize, 2048)
			_ = reader.Close()
			if renderErr != nil {
				writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"code": "preview_unavailable"})
				return
			}
			defer rendered.Cleanup()
			previewReader, openRenderedErr := rendered.Open()
			if openRenderedErr != nil {
				writeLibraryError(w, openRenderedErr)
				return
			}
			objectKey := "library/" + strings.ReplaceAll(uuid.NewString(), "-", "")
			putErr := s.TestingStore.Put(r.Context(), objectKey, previewReader, LibraryObjectMetadata{ByteSize: rendered.ByteSize, SHA256: rendered.SHA256, MIMEType: rendered.MIMEType})
			_ = previewReader.Close()
			if putErr != nil {
				writeLibraryError(w, putErr)
				return
			}
			completed, completeErr := s.database.CompleteLibraryPreview(r.Context(), userID, spaceID, itemID, source.SourceIdentity, objectKey, rendered.MIMEType, rendered.ByteSize, rendered.SHA256, original)
			if completeErr != nil {
				_ = s.TestingStore.Delete(r.Context(), objectKey)
				writeLibraryError(w, completeErr)
				return
			}
			if completed.DiscardObjectKey != "" && completed.ObjectKey != objectKey {
				if _, existingErr := s.TestingStore.Head(r.Context(), completed.ObjectKey); errors.Is(existingErr, ErrLibraryObjectNotFound) {
					if completed.MIMEType != rendered.MIMEType || completed.ByteSize != rendered.ByteSize || completed.SHA256 != rendered.SHA256 {
						_ = s.TestingStore.Delete(r.Context(), objectKey)
						writeJSON(w, http.StatusConflict, map[string]string{"code": "library_preview_mismatch"})
						return
					}
					repairedKey, repairErr := s.database.ReplaceMissingLibraryPreviewDeduplicationObject(r.Context(), userID, spaceID, itemID, source.SourceIdentity, completed.ObjectKey, objectKey)
					if repairErr != nil {
						_ = s.TestingStore.Delete(r.Context(), objectKey)
						writeLibraryError(w, repairErr)
						return
					}
					completed.ObjectKey = repairedKey
					if repairedKey == objectKey {
						completed.DiscardObjectKey = ""
					}
				} else if existingErr != nil {
					_ = s.TestingStore.Delete(r.Context(), objectKey)
					writeLibraryError(w, existingErr)
					return
				}
			}
			if completed.DiscardObjectKey != "" {
				_ = s.TestingStore.Delete(r.Context(), completed.DiscardObjectKey)
			}
			source.PreviewObjectKey, source.PreviewMIME, source.PreviewBytes, source.PreviewSHA256 = completed.ObjectKey, completed.MIMEType, completed.ByteSize, completed.SHA256
		}
		if _, headErr := s.TestingStore.Head(r.Context(), source.PreviewObjectKey); errors.Is(headErr, ErrLibraryObjectNotFound) {
			if s.mediaProcessor == nil {
				writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"code": "preview_unavailable"})
				return
			}
			sourceReader, sourceMetadata, openSourceErr := s.TestingStore.Open(r.Context(), source.ObjectKey)
			if openSourceErr != nil {
				writeLibraryError(w, openSourceErr)
				return
			}
			if sourceMetadata.ByteSize != source.ByteSize || sourceMetadata.SHA256 != source.SHA256 {
				_ = sourceReader.Close()
				writeJSON(w, http.StatusConflict, map[string]string{"code": "library_object_mismatch"})
				return
			}
			rendered, renderErr := s.mediaProcessor.Preview(r.Context(), sourceReader, source.ByteSize, 2048)
			_ = sourceReader.Close()
			if renderErr != nil {
				writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"code": "preview_unavailable"})
				return
			}
			defer rendered.Cleanup()
			if source.PreviewMIME != rendered.MIMEType || source.PreviewBytes != rendered.ByteSize || source.PreviewSHA256 != rendered.SHA256 {
				writeJSON(w, http.StatusConflict, map[string]string{"code": "library_preview_mismatch"})
				return
			}
			previewReader, openRenderedErr := rendered.Open()
			if openRenderedErr != nil {
				writeLibraryError(w, openRenderedErr)
				return
			}
			replacementKey := "library/" + strings.ReplaceAll(uuid.NewString(), "-", "")
			putErr := s.TestingStore.Put(r.Context(), replacementKey, previewReader, LibraryObjectMetadata{ByteSize: rendered.ByteSize, SHA256: rendered.SHA256, MIMEType: rendered.MIMEType})
			_ = previewReader.Close()
			if putErr != nil {
				writeLibraryError(w, putErr)
				return
			}
			repairedKey, repairErr := s.database.ReplaceMissingLibraryPreviewDeduplicationObject(r.Context(), userID, spaceID, itemID, source.SourceIdentity, source.PreviewObjectKey, replacementKey)
			if repairErr != nil {
				_ = s.TestingStore.Delete(r.Context(), replacementKey)
				writeLibraryError(w, repairErr)
				return
			}
			if repairedKey != replacementKey {
				_ = s.TestingStore.Delete(r.Context(), replacementKey)
			}
			source.PreviewObjectKey = repairedKey
		} else if headErr != nil {
			writeLibraryError(w, headErr)
			return
		}
		if TestingWriteLibraryPreviewCacheHeaders(w, r, source.PreviewSHA256) {
			return
		}
		reader, metadata, err := s.TestingStore.Open(r.Context(), source.PreviewObjectKey)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		defer reader.Close()
		if metadata.ByteSize != source.PreviewBytes || metadata.SHA256 != source.PreviewSHA256 {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "library_object_mismatch"})
			return
		}
		w.Header().Set("Content-Type", source.PreviewMIME)
		w.Header().Set("Content-Length", strconv.FormatInt(source.PreviewBytes, 10))
		w.Header().Set("Content-Disposition", "inline")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, reader)
	}
}

func TestingWriteLibraryPreviewCacheHeaders(w http.ResponseWriter, r *http.Request, sha string) bool {
	etag := `"` + sha + `"`
	w.Header().Set("ETag", etag)
	w.Header().Add("Vary", "Authorization, X-Misty-Library-Reauthentication")
	if strings.TrimSpace(r.URL.Query().Get("cache_version")) != "" {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			w.WriteHeader(http.StatusNotModified)
			return true
		}
	}
	return false
}

func (s *SpaceLibraryService) validateLibraryReauthentication(r *http.Request, userID, spaceID, scope string) error {
	token := strings.TrimSpace(r.Header.Get(libraryReauthenticationHeader))
	if token == "" {
		return db.ErrLibraryReauthentication
	}
	return s.database.ValidateLibraryReauthenticationGrant(r.Context(), userID, spaceID, scope, security.HashToken(token))
}

func (s *SpaceLibraryService) validateSensitiveLibraryItem(r *http.Request, userID, spaceID, itemID string) error {
	scope, err := s.database.SensitiveLibraryItemScope(r.Context(), userID, spaceID, itemID)
	if err != nil || scope == "" {
		return err
	}
	return s.validateLibraryReauthentication(r, userID, spaceID, scope)
}

func (s *SpaceLibraryService) validateSensitiveLibraryAssetStack(r *http.Request, userID, spaceID, stackID string) error {
	scope, err := s.database.SensitiveLibraryAssetStackScope(r.Context(), userID, spaceID, stackID)
	if err != nil || scope == "" {
		return err
	}
	return s.validateLibraryReauthentication(r, userID, spaceID, scope)
}

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
		s.TestingWriteDownload(w, r, download)
	}
}

func (s *SpaceLibraryService) PromoteAttachment() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		item, err := s.database.PromoteMessageAttachment(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "attachmentID"))
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	}
}
