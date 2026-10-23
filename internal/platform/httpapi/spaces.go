package api

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *SpacesService) Spaces() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			entitlements, err := s.database.EntitlementsForUser(r.Context(), userID)
			if err != nil {
				writeSpaceError(w, fmt.Errorf("load entitlements: %w", err))
				return
			}
			spaces, err := s.database.ListSpaces(r.Context(), userID)
			if err != nil {
				writeSpaceError(w, fmt.Errorf("list spaces: %w", err))
				return
			}
			invites, err := s.database.IncomingSpaceInvites(r.Context(), userID)
			if err != nil {
				writeSpaceError(w, fmt.Errorf("list incoming space invites: %w", err))
				return
			}
			storage, err := s.database.OwnerStorageUsage(r.Context(), userID)
			if err != nil {
				writeSpaceError(w, fmt.Errorf("load owner storage usage: %w", err))
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"spaces": spaces, "invitations": invites,
				"entitlements":  entitlements,
				"owner_storage": storage})
		case http.MethodPost:
			var body struct {
				Name string `json:"name"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			result, err := s.database.CreateSpace(r.Context(), userID, body.Name, r.Header.Get("Idempotency-Key"))
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, result)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) Space() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			space, err := s.database.SpaceByID(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, space)
		case http.MethodPatch, http.MethodPut:
			var body struct {
				Name string `json:"name"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			space, err := s.database.RenameSpace(r.Context(), userID, spaceID, body.Name)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, space)
		case http.MethodDelete:
			var body struct {
				Confirmation string `json:"confirmation"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			if err := s.database.DeleteSpace(r.Context(), userID, spaceID, body.Confirmation); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}
