package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

func (s *SpacesService) Roles() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			roles, err := s.database.SpaceRoles(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, roles)
			return
		}
		var body db.SpaceRoleInput
		if decodeJSON(w, r, &body) != nil {
			return
		}
		role, err := s.database.CreateSpaceRole(r.Context(), userID, spaceID, body)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, role)
	}
}

func (s *SpacesService) Role() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, roleID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "roleID")
		if r.Method == http.MethodDelete {
			if err := s.database.DeleteSpaceRole(r.Context(), userID, spaceID, roleID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body db.SpaceRoleInput
		if decodeJSON(w, r, &body) != nil {
			return
		}
		role, err := s.database.UpdateSpaceRole(r.Context(), userID, spaceID, roleID, body)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, role)
	}
}

func (s *SpacesService) RoleOrder() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			RoleIDs []string `json:"role_ids"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		roles, err := s.database.ReorderSpaceRoles(r.Context(), userID, chi.URLParam(r, "spaceID"), body.RoleIDs)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, roles)
	}
}

func (s *SpacesService) MemberRoles() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			RoleIDs []string `json:"role_ids"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		member, err := s.database.SetSpaceMemberRoles(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "userID"), body.RoleIDs)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, member)
	}
}

func (s *SpacesService) ConversationOverwrites() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Overwrites []db.SpaceConversationOverwrite `json:"overwrites"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		conversation, err := s.database.SetSpaceConversationOverwrites(r.Context(), userID,
			chi.URLParam(r, "spaceID"), chi.URLParam(r, "conversationID"), body.Overwrites)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, conversation)
	}
}
