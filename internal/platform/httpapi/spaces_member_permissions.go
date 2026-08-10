package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// MemberPermissions lets a Space owner inspect and update one member's
// effective permissions. Ordinary members may inspect only their own access.
func (s *SpacesService) MemberPermissions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		memberID := chi.URLParam(r, "userID")

		switch r.Method {
		case http.MethodGet:
			permissions, err := s.database.SpaceMemberPermissions(
				r.Context(),
				userID,
				spaceID,
				memberID,
			)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"permissions": permissions})
		case http.MethodPut:
			var body struct {
				Permission string `json:"permission"`
				Effect     string `json:"effect"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			if err := s.database.SetSpaceMemberPermission(
				r.Context(),
				userID,
				spaceID,
				memberID,
				body.Permission,
				body.Effect,
			); err != nil {
				writeSpaceError(w, err)
				return
			}
			permissions, err := s.database.SpaceMemberPermissions(
				r.Context(),
				userID,
				spaceID,
				memberID,
			)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"permissions": permissions})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}
