package api

import (
	"archive/zip"
	"io"
	"mime"
	"net/http"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
)

func (s *SpaceLibraryService) Facets() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		facets, err := s.database.LibraryFacets(r.Context(), userID, chi.URLParam(r, "spaceID"), r.URL.Query().Get("q"))
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, facets)
	}
}

func (s *SpaceLibraryService) Discovery() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		discovery, err := s.database.LibraryDiscovery(r.Context(), userID, chi.URLParam(r, "spaceID"))
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		if !s.locationsEnabled {
			discovery.Trips = []db.LibraryDiscoveryGroup{}
		}
		if !s.duplicatesEnabled {
			discovery.Duplicates = []db.LibraryDiscoveryGroup{}
		}
		writeJSON(w, http.StatusOK, discovery)
	}
}

func (s *SpaceLibraryService) DiscoveryItems() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		kind := chi.URLParam(r, "kind")
		if (kind == "trip" || kind == "map") && !s.locationsEnabled || kind == "duplicate" && !s.duplicatesEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_discovery_disabled"})
			return
		}
		items, err := s.database.LibraryDiscoveryItems(r.Context(), userID, chi.URLParam(r, "spaceID"), kind, chi.URLParam(r, "groupID"))
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func (s *SpaceLibraryService) MemoryPreference() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Version         int64   `json:"version"`
			Title           string  `json:"title"`
			CoverItemID     string  `json:"cover_item_id"`
			MusicItemID     string  `json:"music_item_id"`
			PlaybackSeconds float64 `json:"playback_seconds"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		spaceID, memoryID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "memoryID")
		if err := s.database.UpdateLibraryMemoryPreference(r.Context(), userID, spaceID, memoryID, body.Version, body.Title, body.CoverItemID, body.MusicItemID, body.PlaybackSeconds); err != nil {
			writeLibraryError(w, err)
			return
		}
		discovery, err := s.database.LibraryDiscovery(r.Context(), userID, spaceID)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		for _, memory := range discovery.Memories {
			if memory.ID == memoryID {
				writeJSON(w, http.StatusOK, memory)
				return
			}
		}
		writeLibraryError(w, db.ErrLibraryNotFound)
	}
}

func (s *SpaceLibraryService) MergeDuplicates() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if !s.duplicatesEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_duplicates_disabled"})
			return
		}
		var body struct {
			Keeper     db.LibraryItemVersion   `json:"keeper"`
			Duplicates []db.LibraryItemVersion `json:"duplicates"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if err := s.validateSensitiveLibraryItem(r, userID, spaceID, body.Keeper.ID); err != nil {
			writeLibraryError(w, err)
			return
		}
		for _, duplicate := range body.Duplicates {
			if err := s.validateSensitiveLibraryItem(r, userID, spaceID, duplicate.ID); err != nil {
				writeLibraryError(w, err)
				return
			}
		}
		item, err := s.database.MergeLibraryDuplicates(r.Context(), userID, spaceID, body.Keeper, body.Duplicates)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func (s *SpaceLibraryService) ExportItems() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if !s.exportsEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_exports_disabled"})
			return
		}
		var body struct {
			ItemIDs []string `json:"item_ids"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if err := s.validateLibraryReauthentication(r, userID, spaceID, "bulk_export"); err != nil {
			writeLibraryError(w, err)
			return
		}
		items, err := s.database.LibraryTransferItems(r.Context(), userID, spaceID, body.ItemIDs)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		var total int64
		for _, item := range items {
			total += item.ByteSize
			metadata, headErr := s.TestingStore.Head(r.Context(), item.ObjectKey)
			if headErr != nil || metadata.ByteSize != item.ByteSize || metadata.SHA256 != item.SHA256 {
				writeJSON(w, http.StatusConflict, map[string]string{"code": "library_object_mismatch"})
				return
			}
		}
		if total > 500_000_000 {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"code": "library_export_too_large"})
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "misty-library-export-" + time.Now().Format("20060102") + ".zip"}))
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		archive := zip.NewWriter(w)
		usedNames := map[string]int{}
		for _, item := range items {
			reader, _, openErr := s.TestingStore.Open(r.Context(), item.ObjectKey)
			if openErr != nil {
				_ = archive.Close()
				return
			}
			filename := item.Filename
			if item.Rendition {
				filename = libraryRenditionFilename(filename, item.MIMEType)
			}
			name := uniqueArchiveName(sanitizeLibraryFilename(filename), usedNames)
			entry, createErr := archive.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
			if createErr == nil {
				_, createErr = io.Copy(entry, io.LimitReader(reader, item.ByteSize+1))
			}
			_ = reader.Close()
			if createErr != nil {
				_ = archive.Close()
				return
			}
		}
		_ = archive.Close()
	}
}
