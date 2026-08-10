package api

import (
	"net/http"
	"strconv"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
)

func (s *SpaceLibraryService) ImportItems() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if !s.importsEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_imports_disabled"})
			return
		}
		var body struct {
			DestinationSpaceID string   `json:"destination_space_id"`
			ItemIDs            []string `json:"item_ids"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		sourceSpaceID := chi.URLParam(r, "spaceID")
		if body.DestinationSpaceID == "" || body.DestinationSpaceID == sourceSpaceID || len(body.ItemIDs) < 1 || len(body.ItemIDs) > 50 {
			writeLibraryError(w, db.ErrLibraryInvalid)
			return
		}
		allowed, err := s.database.HasSpacePermission(r.Context(), userID, body.DestinationSpaceID, db.PermissionLibraryImport)
		if err != nil || !allowed {
			writeLibraryError(w, db.ErrLibraryForbidden)
			return
		}
		items, err := s.database.LibraryTransferItems(r.Context(), userID, sourceSpaceID, body.ItemIDs)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		for _, itemID := range body.ItemIDs {
			if err := s.validateSensitiveLibraryItem(r, userID, sourceSpaceID, itemID); err != nil {
				writeLibraryError(w, err)
				return
			}
		}
		usage, err := s.database.SpaceStorageUsage(r.Context(), userID, body.DestinationSpaceID)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		var required int64
		for _, item := range items {
			required += item.ByteSize
		}
		if required > usage.RemainingBytes {
			writeLibraryError(w, db.ErrLibraryQuota)
			return
		}
		imported := make([]db.SpaceLibraryItem, 0, len(items))
		for _, item := range items {
			result, importErr := s.copyLibraryItem(r.Context(), userID, sourceSpaceID, body.DestinationSpaceID, item, true)
			if importErr != nil {
				writeLibraryError(w, importErr)
				return
			}
			imported = append(imported, *result)
		}
		writeJSON(w, http.StatusCreated, map[string]any{"items": imported})
	}
}

func (s *SpaceLibraryService) DuplicateItems() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			ItemIDs []string `json:"item_ids"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if len(body.ItemIDs) < 1 || len(body.ItemIDs) > 50 {
			writeLibraryError(w, db.ErrLibraryInvalid)
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		allowed, err := s.database.HasSpacePermission(r.Context(), userID, spaceID, db.PermissionLibraryEdit)
		if err != nil || !allowed {
			writeLibraryError(w, db.ErrLibraryForbidden)
			return
		}
		items, err := s.database.LibraryTransferItems(r.Context(), userID, spaceID, body.ItemIDs)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		for _, itemID := range body.ItemIDs {
			if err := s.validateSensitiveLibraryItem(r, userID, spaceID, itemID); err != nil {
				writeLibraryError(w, err)
				return
			}
		}
		usage, err := s.database.SpaceStorageUsage(r.Context(), userID, spaceID)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		var required int64
		for _, source := range items {
			required += source.ByteSize
		}
		if required > usage.RemainingBytes {
			writeLibraryError(w, db.ErrLibraryQuota)
			return
		}
		duplicated := make([]db.SpaceLibraryItem, 0, len(items))
		for _, source := range items {
			item, err := s.copyLibraryItem(r.Context(), userID, spaceID, spaceID, source, false)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			duplicated = append(duplicated, *item)
		}
		writeJSON(w, http.StatusCreated, map[string]any{"items": duplicated})
	}
}

func (s *SpaceLibraryService) SharedReferences() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if !s.importsEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_sharing_disabled"})
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			items, err := s.database.LibrarySharedReferences(r.Context(), userID, spaceID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			outgoing, _ := s.database.LibraryOutgoingGrants(r.Context(), userID, spaceID)
			writeJSON(w, http.StatusOK, map[string]any{"references": items, "outgoing": outgoing})
		case http.MethodPost:
			var body struct {
				DestinationSpaceID string   `json:"destination_space_id"`
				ItemIDs            []string `json:"item_ids"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			if len(body.ItemIDs) < 1 || len(body.ItemIDs) > 50 {
				writeLibraryError(w, db.ErrLibraryInvalid)
				return
			}
			items := make([]db.LibrarySharedReference, 0, len(body.ItemIDs))
			for _, itemID := range body.ItemIDs {
				if err := s.validateSensitiveLibraryItem(r, userID, spaceID, itemID); err != nil {
					writeLibraryError(w, err)
					return
				}
				item, err := s.database.CreateLibraryGrant(r.Context(), userID, spaceID, itemID, body.DestinationSpaceID)
				if err != nil {
					writeLibraryError(w, err)
					return
				}
				items = append(items, *item)
			}
			writeJSON(w, http.StatusCreated, map[string]any{"references": items})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpaceLibraryService) SharedReferenceDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		download, err := s.database.LibrarySharedReferenceDownload(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "referenceID"))
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		s.TestingWriteDownload(w, r, download)
	}
}

func (s *SpaceLibraryService) RevokeGrant() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		version, err := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
		if err != nil {
			writeLibraryError(w, db.ErrLibraryInvalid)
			return
		}
		if err := s.database.RevokeLibraryGrant(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "grantID"), version); err != nil {
			writeLibraryError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
