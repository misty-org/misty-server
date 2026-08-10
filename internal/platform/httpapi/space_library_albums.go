package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

func (s *SpaceLibraryService) Albums() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			albums, err := s.database.LibraryAlbums(r.Context(), userID, spaceID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"albums": albums})
		case http.MethodPost:
			var body struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			album, err := s.database.CreateLibraryAlbum(r.Context(), userID, spaceID, body.Name, body.Description)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, album)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpaceLibraryService) Album() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, albumID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "albumID")
		switch r.Method {
		case http.MethodGet:
			album, err := s.database.LibraryAlbum(r.Context(), userID, spaceID, albumID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, album)
		case http.MethodPatch:
			var body struct {
				Version     int64  `json:"version"`
				Name        string `json:"name"`
				Description string `json:"description"`
				CoverItemID string `json:"cover_item_id"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			album, err := s.database.UpdateLibraryAlbum(r.Context(), userID, spaceID, albumID, body.Version, body.Name, body.Description, body.CoverItemID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, album)
		case http.MethodDelete:
			version, _ := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
			if err := s.database.DeleteLibraryAlbum(r.Context(), userID, spaceID, albumID, version); err != nil {
				writeLibraryError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpaceLibraryService) OrganizeAlbum() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Version  int64  `json:"version"`
			FolderID string `json:"folder_id"`
			ViewMode string `json:"view_mode"`
			SortMode string `json:"sort_mode"`
			Position int64  `json:"position"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		album, err := s.database.OrganizeLibraryAlbum(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "albumID"), body.Version, body.FolderID, body.ViewMode, body.SortMode, body.Position)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, album)
	}
}

func (s *SpaceLibraryService) AlbumFolders() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			folders, err := s.database.LibraryAlbumFolders(r.Context(), userID, spaceID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"folders": folders})
		case http.MethodPost:
			var body struct {
				Name           string `json:"name"`
				ParentFolderID string `json:"parent_folder_id"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			folder, err := s.database.CreateLibraryAlbumFolder(r.Context(), userID, spaceID, body.ParentFolderID, body.Name)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, folder)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpaceLibraryService) AlbumFolder() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, folderID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "folderID")
		switch r.Method {
		case http.MethodPatch:
			var body struct {
				Version        int64  `json:"version"`
				Name           string `json:"name"`
				ParentFolderID string `json:"parent_folder_id"`
				Position       int64  `json:"position"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			folder, err := s.database.UpdateLibraryAlbumFolder(r.Context(), userID, spaceID, folderID, body.Version, body.ParentFolderID, body.Name, body.Position)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, folder)
		case http.MethodDelete:
			version, _ := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
			if err := s.database.DeleteLibraryAlbumFolder(r.Context(), userID, spaceID, folderID, version); err != nil {
				writeLibraryError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpaceLibraryService) ReorderAlbumItems() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Version int64    `json:"version"`
			ItemIDs []string `json:"item_ids"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		album, err := s.database.ReorderLibraryAlbumItems(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "albumID"), body.Version, body.ItemIDs)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, album)
	}
}

func (s *SpaceLibraryService) AlbumItems() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, albumID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "albumID")
		switch r.Method {
		case http.MethodGet:
			items, err := s.database.LibraryAlbumItems(r.Context(), userID, spaceID, albumID, 200)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
		case http.MethodPost:
			var body struct {
				ItemIDs []string `json:"item_ids"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			if err := s.database.AddLibraryAlbumItems(r.Context(), userID, spaceID, albumID, body.ItemIDs); err != nil {
				writeLibraryError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			if err := s.database.RemoveLibraryAlbumItem(r.Context(), userID, spaceID, albumID, chi.URLParam(r, "itemID")); err != nil {
				writeLibraryError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}
