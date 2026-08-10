package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// SpaceDrawings lists and creates first-class collaborative drawings.
func (s *SpacesService) SpaceDrawings() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			drawings, err := s.database.AccessibleSpaceDrawings(
				r.Context(), userID, spaceID,
			)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"drawings": drawings})
		case http.MethodPost:
			var body struct {
				Title string `json:"title"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			drawing, err := s.database.CreateSpaceDrawing(
				r.Context(), userID, spaceID, body.Title,
			)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, drawing)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// SpaceDrawing reads, renames, or deletes one drawing. Requests made under the
// wrong Space receive the same response as a missing drawing.
func (s *SpacesService) SpaceDrawing() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		drawingID := chi.URLParam(r, "drawingID")
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			drawing, err := s.database.SpaceDrawingByID(
				r.Context(), userID, drawingID,
			)
			if err != nil || drawing.SpaceID != spaceID {
				if err == nil {
					err = db.ErrSpaceNotFound
				}
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, drawing)
		case http.MethodPatch:
			var body struct {
				Title string `json:"title"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			current, err := s.database.SpaceDrawingByID(
				r.Context(), userID, drawingID,
			)
			if err != nil || current.SpaceID != spaceID {
				if err == nil {
					err = db.ErrSpaceNotFound
				}
				writeSpaceError(w, err)
				return
			}
			drawing, err := s.database.RenameSpaceDrawing(
				r.Context(), userID, drawingID, body.Title,
			)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, drawing)
		case http.MethodDelete:
			current, err := s.database.SpaceDrawingByID(
				r.Context(), userID, drawingID,
			)
			if err != nil || current.SpaceID != spaceID {
				if err == nil {
					err = db.ErrSpaceNotFound
				}
				writeSpaceError(w, err)
				return
			}
			if err := s.database.DeleteSpaceDrawing(
				r.Context(), userID, drawingID,
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

// SpaceDrawingCollaborationTicket mints a short-lived credential after
// rechecking current Space membership and drawing state.
func (s *SpacesService) SpaceDrawingCollaborationTicket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		drawingID := chi.URLParam(r, "drawingID")
		drawing, err := s.database.SpaceDrawingByID(
			r.Context(), userID, drawingID,
		)
		if err != nil || drawing.SpaceID != chi.URLParam(r, "spaceID") {
			if err == nil {
				err = db.ErrSpaceNotFound
			}
			writeSpaceError(w, err)
			return
		}
		ticket, err := s.TestingJournalCollab.MintDrawingTicket(
			userID,
			drawing.SpaceID,
			drawing.ID,
			drawing.Role,
			drawing.ACLVersion,
		)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusCreated, ticket)
	}
}
