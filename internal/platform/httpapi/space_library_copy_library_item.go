package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func (s *SpaceLibraryService) copyLibraryItem(ctx context.Context, userID, sourceSpaceID, destinationSpaceID string, source db.LibraryTransferItem, recordImport bool) (*db.SpaceLibraryItem, error) {
	token, err := security.GenerateSecureToken()
	if err != nil {
		return nil, err
	}
	tokenHash := security.HashToken(token)
	objectKey := "library/" + strings.ReplaceAll(uuid.NewString(), "-", "")
	filename := source.Filename
	if source.Rendition {
		filename = libraryRenditionFilename(filename, source.MIMEType)
	}
	upload, err := s.database.CreateLibraryUpload(ctx, userID, destinationSpaceID, "library", filename, source.MIMEType, source.ByteSize, source.SHA256, objectKey, tokenHash, time.Now().Add(libraryUploadLifetime).UTC())
	if err != nil {
		return nil, err
	}
	if _, err = s.database.SetLibraryUploadState(ctx, userID, destinationSpaceID, upload.ID, tokenHash, "initiated", "uploading"); err != nil {
		return nil, err
	}
	reader, _, err := s.TestingStore.Open(ctx, source.ObjectKey)
	if err != nil {
		s.rejectAndDelete(ctx, upload, tokenHash, "invalid", "import_source_missing")
		return nil, err
	}
	putErr := s.TestingStore.Put(ctx, objectKey, io.LimitReader(reader, source.ByteSize+1), LibraryObjectMetadata{ByteSize: source.ByteSize, SHA256: source.SHA256, MIMEType: source.MIMEType})
	_ = reader.Close()
	if putErr != nil {
		s.rejectAndDelete(ctx, upload, tokenHash, "invalid", "import_copy_failed")
		return nil, putErr
	}
	if _, err = s.database.SetLibraryUploadState(ctx, userID, destinationSpaceID, upload.ID, tokenHash, "uploading", "uploaded_unverified"); err != nil {
		s.rejectAndDelete(ctx, upload, tokenHash, "invalid", "import_state_failed")
		return nil, err
	}
	intrinsicMetadata := source.IntrinsicMetadata
	if source.Rendition {
		intrinsicMetadata = libraryRenditionIntrinsicMetadata(source)
	}
	completed, err := s.database.CompleteLibraryUpload(ctx, userID, destinationSpaceID, upload.ID, tokenHash, source.ByteSize, source.SHA256, source.MIMEType, intrinsicMetadata)
	if err != nil {
		s.rejectAndDelete(ctx, upload, tokenHash, "invalid", "import_finalize_failed")
		return nil, err
	}
	if completed.DiscardObjectKey != "" {
		_ = s.TestingStore.Delete(ctx, completed.DiscardObjectKey)
	}
	if completed.Item == nil {
		return nil, db.ErrLibraryConflict
	}
	if recordImport {
		if _, err := s.database.RecordLibraryImport(ctx, userID, sourceSpaceID, source.ItemID, destinationSpaceID, completed.Item.ID, upload.ID, source.ByteSize); err != nil {
			return nil, err
		}
	} else if err := s.database.RecordLibraryDuplicate(ctx, userID, destinationSpaceID, source.ItemID, completed.Item.ID, source.ByteSize); err != nil {
		return nil, err
	}
	return completed.Item, nil
}

func uniqueArchiveName(filename string, used map[string]int) string {
	if filename == "" {
		filename = "item"
	}
	count := used[strings.ToLower(filename)]
	used[strings.ToLower(filename)] = count + 1
	if count == 0 {
		return filename
	}
	extension := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, extension)
	return fmt.Sprintf("%s (%d)%s", base, count+1, extension)
}

func (s *SpaceLibraryService) Item() http.HandlerFunc {
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
		switch r.Method {
		case http.MethodGet:
			item, err := s.database.LibraryItem(r.Context(), userID, spaceID, itemID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
		case http.MethodPatch:
			var body struct {
				Version     int64    `json:"version"`
				DisplayName string   `json:"display_name"`
				Caption     string   `json:"caption"`
				Tags        []string `json:"tags"`
				Favorite    bool     `json:"favorite"`
				Hidden      bool     `json:"hidden"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			item, err := s.database.UpdateLibraryItem(r.Context(), userID, spaceID, itemID, body.Version, body.DisplayName, body.Caption, body.Tags, body.Favorite, body.Hidden)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpaceLibraryService) BulkItems() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Action           string                  `json:"action"`
			Items            []db.LibraryItemVersion `json:"items"`
			AlbumID          string                  `json:"album_id"`
			Tags             []string                `json:"tags"`
			DateOverride     string                  `json:"date_override"`
			LocationOverride json.RawMessage         `json:"location_override"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		for _, item := range body.Items {
			if err := s.validateSensitiveLibraryItem(r, userID, spaceID, item.ID); err != nil {
				writeLibraryError(w, err)
				return
			}
		}
		if body.Action == "restore" {
			if err := s.validateLibraryReauthentication(r, userID, spaceID, "recently_deleted"); err != nil {
				writeLibraryError(w, err)
				return
			}
		}
		operation := db.BulkLibraryItemOperation{Action: body.Action, Items: body.Items, AlbumID: body.AlbumID, Tags: body.Tags, LocationOverride: body.LocationOverride}
		if body.DateOverride != "" {
			parsed, err := time.Parse(time.RFC3339, body.DateOverride)
			if err != nil {
				writeLibraryError(w, db.ErrLibraryInvalid)
				return
			}
			operation.DateOverride = &parsed
		}
		items, err := s.database.BulkUpdateLibraryItems(r.Context(), userID, spaceID, operation)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func (s *SpaceLibraryService) TrashItem() http.HandlerFunc {
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
		item, err := s.database.TrashLibraryItem(r.Context(), userID, spaceID, itemID)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func (s *SpaceLibraryService) RestoreItem() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, itemID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "itemID")
		if err := s.validateLibraryReauthentication(r, userID, spaceID, "recently_deleted"); err != nil {
			writeLibraryError(w, err)
			return
		}
		item, err := s.database.RestoreLibraryItem(r.Context(), userID, spaceID, itemID)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func (s *SpaceLibraryService) CleanupExpired(ctx context.Context, limit int) (int, error) {
	uploads, err := s.database.ExpireLibraryUploads(ctx, limit)
	if err != nil {
		return 0, err
	}
	for _, upload := range uploads {
		if err := s.TestingStore.Delete(ctx, upload.ObjectKey); err != nil && !errors.Is(err, ErrLibraryObjectNotFound) {
			return len(uploads), err
		}
	}
	_, err = s.database.ReconcileLibraryStorageUsage(ctx, limit)
	return len(uploads), err
}
