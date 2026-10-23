package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *SpacesService) Members() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		members, err := s.database.SpaceMembers(r.Context(), userID, spaceID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"members": members})
	}
}

func (s *SpacesService) RemoveMember() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.RemoveSpaceMember(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "userID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpacesService) LeaveSpace() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.LeaveSpace(r.Context(), userID, chi.URLParam(r, "spaceID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpacesService) TransferOwner() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			UserID string `json:"user_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if err := s.database.TransferSpaceOwnership(r.Context(), userID, chi.URLParam(r, "spaceID"), body.UserID); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
