package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// SpaceIntegrations exposes the approved connections visible to a Space.
// Creating or changing credentials is intentionally handled only by the
// branded authorization flows mounted elsewhere.
func (s *SpacesService) SpaceIntegrations() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		items, err := s.database.SpaceIntegrations(r.Context(), userID, chi.URLParam(r, "spaceID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"integrations": items,
			"providers":    TestingProviderOAuthAvailabilityCatalog(),
		})
	}
}
