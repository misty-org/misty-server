package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (s *SpaceLibraryService) SetMediaProcessor(processor LibraryMediaProcessor) {
	s.mediaProcessor = processor
}

func (s *SpaceLibraryService) RenderEditVersion() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.editingEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_editing_disabled"})
			return
		}
		if s.mediaProcessor == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_media_processor_unavailable"})
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
			MaximumOutputBytes int64 `json:"maximum_output_bytes"`
		}
		if r.ContentLength > 0 && decodeJSON(w, r, &body) != nil {
			return
		}
		result, err := s.database.QueueLibraryEditRendition(r.Context(), userID, spaceID, itemID, chi.URLParam(r, "editID"), body.MaximumOutputBytes)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		status := http.StatusAccepted
		if result.State == "ready" {
			status = http.StatusOK
		}
		writeJSON(w, status, result)
	}
}

func (s *SpaceLibraryService) ProcessRenditionJobs(ctx context.Context, workerID string, limit int) (int, error) {
	if s.mediaProcessor == nil || !s.editingEnabled || limit < 1 {
		return 0, nil
	}
	if limit > 8 {
		limit = 8
	}
	processed := 0
	for processed < limit {
		job, err := s.database.ClaimLibraryRenditionJob(ctx, workerID, 15*time.Minute)
		if err != nil {
			return processed, err
		}
		if job == nil {
			return processed, nil
		}
		reader, metadata, err := s.TestingStore.Open(ctx, job.SourceObjectKey)
		if err != nil || metadata.ByteSize != job.SourceBytes || metadata.SHA256 != job.SourceSHA256 {
			if reader != nil {
				_ = reader.Close()
			}
			_ = s.database.FailLibraryRenditionJob(ctx, job, "rendition_source_unavailable")
			processed++
			continue
		}
		rendered, renderErr := s.mediaProcessor.Render(ctx, reader, job.SourceMIME, job.SourceBytes, job.Definition, job.ReservedBytes)
		_ = reader.Close()
		if renderErr != nil {
			_ = s.database.FailLibraryRenditionJob(ctx, job, "rendition_processor_failed")
			processed++
			continue
		}
		objectKey := "library/rendition_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		output, openErr := rendered.Open()
		if openErr != nil {
			rendered.Cleanup()
			_ = s.database.FailLibraryRenditionJob(ctx, job, "rendition_output_unavailable")
			processed++
			continue
		}
		putErr := s.TestingStore.Put(ctx, objectKey, output, LibraryObjectMetadata{ByteSize: rendered.ByteSize, SHA256: rendered.SHA256, MIMEType: rendered.MIMEType})
		_ = output.Close()
		if putErr != nil {
			rendered.Cleanup()
			_ = s.database.FailLibraryRenditionJob(ctx, job, "rendition_store_failed")
			processed++
			continue
		}
		completed, completeErr := s.database.CompleteLibraryRenditionJob(ctx, job, objectKey, rendered.MIMEType, rendered.ByteSize, rendered.SHA256)
		rendered.Cleanup()
		if completeErr != nil {
			_ = s.TestingStore.Delete(ctx, objectKey)
			_ = s.database.FailLibraryRenditionJob(ctx, job, "rendition_finalize_failed")
			processed++
			continue
		}
		if completed.DiscardObjectKey != "" {
			if err := s.TestingStore.Delete(ctx, completed.DiscardObjectKey); err != nil && !errors.Is(err, ErrLibraryObjectNotFound) {
				return processed, err
			}
		}
		processed++
	}
	return processed, nil
}

func (s *SpaceLibraryService) PurgeExpiredRenditions(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	purged := 0
	for purged < limit {
		candidate, err := s.database.ClaimExpiredLibraryRenditionPurge(ctx, 5*time.Minute)
		if err != nil {
			return purged, err
		}
		if candidate == nil {
			return purged, nil
		}
		if candidate.ObjectKey != "" {
			if err := s.TestingStore.Delete(ctx, candidate.ObjectKey); err != nil && !errors.Is(err, ErrLibraryObjectNotFound) {
				_ = s.database.FailLibraryRenditionPurge(ctx, candidate)
				return purged, err
			}
		}
		if err := s.database.CompleteLibraryRenditionPurge(ctx, candidate); err != nil {
			_ = s.database.FailLibraryRenditionPurge(ctx, candidate)
			return purged, err
		}
		purged++
	}
	return purged, nil
}
