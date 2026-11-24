package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

func (s *SpacesService) SpaceNotes() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Has("q") {
				text, valid := spaceSearchText(r)
				if !valid {
					writeSpaceError(w, db.ErrSpaceInvalid)
					return
				}
				notes, err := s.database.SearchSpaceNotes(r.Context(), userID, spaceID, text, spaceSearchLimit(r))
				if err != nil {
					writeSpaceError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"notes": notes})
				return
			}
			notes, err := s.database.AccessibleSpaceNotes(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"notes": notes})
		case http.MethodPost:
			s.createSpaceNote(w, r, userID, spaceID)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) SpaceNote() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		noteID := chi.URLParam(r, "noteID")
		switch r.Method {
		case http.MethodGet:
			note, err := s.database.SpaceNoteByID(r.Context(), userID, noteID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			if note.SpaceID != chi.URLParam(r, "spaceID") {
				writeSpaceError(w, db.ErrSpaceNotFound)
				return
			}
			writeJSON(w, http.StatusOK, note)
		case http.MethodDelete:
			if err := s.database.DeleteSpaceNote(r.Context(), userID, noteID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPatch:
			var body struct {
				Archived *bool `json:"archived"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			if body.Archived == nil {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			if err := s.database.SetSpaceNoteArchived(
				r.Context(), userID, noteID, *body.Archived,
			); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) SpaceNoteMetadata() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			SharedTags []string `json:"shared_tags"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		noteID := chi.URLParam(r, "noteID")
		if err := s.database.UpdateNoteSharedTags(r.Context(), userID, noteID, body.SharedTags); err != nil {
			writeSpaceError(w, err)
			return
		}
		note, err := s.database.SpaceNoteByID(r.Context(), userID, noteID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, note)
	}
}

func (s *SpacesService) SpaceNoteBacklinks() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		noteID := chi.URLParam(r, "noteID")
		note, err := s.database.SpaceNoteByID(r.Context(), userID, noteID)
		if err != nil || note.SpaceID != chi.URLParam(r, "spaceID") {
			writeSpaceError(w, db.ErrSpaceNotFound)
			return
		}
		links, err := s.database.SpaceNoteBacklinks(r.Context(), userID, noteID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"backlinks": links})
	}
}
