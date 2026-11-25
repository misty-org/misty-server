package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

func (s *SpacesService) ChannelCategories() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.CreateSpaceChannelCategory(r.Context(), userID, chi.URLParam(r, "spaceID"), body.Name)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	}
}

func (s *SpacesService) ChannelCategory() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		categoryID := chi.URLParam(r, "categoryID")
		if r.Method == http.MethodDelete {
			if err := s.database.DeleteSpaceChannelCategory(r.Context(), userID, spaceID, categoryID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body struct {
			Name    *string `json:"name"`
			AfterID *string `json:"after_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.UpdateSpaceChannelCategory(r.Context(), userID, spaceID, categoryID,
			db.SpaceChannelCategoryChange{Name: body.Name, AfterID: body.AfterID})
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func (s *SpacesService) ConversationPlace() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			CategoryID string `json:"category_id"`
			AfterID    string `json:"after_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.PlaceSpaceChannel(r.Context(), userID, chi.URLParam(r, "spaceID"),
			chi.URLParam(r, "conversationID"), body.CategoryID, body.AfterID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}
