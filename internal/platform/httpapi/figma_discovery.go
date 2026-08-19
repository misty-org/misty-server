package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) FigmaProjects() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		token, _, err := s.connectedAccountAccessTokenForCapability(r.Context(), userID, r.URL.Query().Get("connection_id"), "drawings_projects")
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		items, err := s.figmaProvider(token).Projects(r.Context(), chi.URLParam(r, "teamID"))
		if err != nil {
			writeFigmaDiscoveryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"projects": items, "availability": "private_app_only"})
	}
}

func (s *SpacesService) FigmaProjectFiles() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		token, _, err := s.connectedAccountAccessTokenForCapability(r.Context(), userID, r.URL.Query().Get("connection_id"), "drawings_projects")
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		items, err := s.figmaProvider(token).ProjectFiles(r.Context(), chi.URLParam(r, "projectID"))
		if err != nil {
			writeFigmaDiscoveryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"files": items, "availability": "private_app_only"})
	}
}

func writeFigmaError(w http.ResponseWriter, err error) {
	code := http.StatusBadGateway
	errorCode := "figma_api_error"
	if err != nil && strings.Contains(err.Error(), "too_large") {
		code = http.StatusRequestEntityTooLarge
		errorCode = "figma_response_too_large"
	} else if err != nil && strings.Contains(err.Error(), "figma_api_429") {
		code = http.StatusTooManyRequests
		errorCode = "figma_rate_limited"
	}
	var providerErr *figmaAPIError
	if errors.As(err, &providerErr) && providerErr.RetryAfter != "" {
		w.Header().Set("Retry-After", providerErr.RetryAfter)
	}
	writeJSON(w, code, map[string]string{"code": errorCode})
}

func writeFigmaDiscoveryError(w http.ResponseWriter, err error) {
	if err != nil && (strings.Contains(err.Error(), "figma_api_403") || strings.Contains(err.Error(), "figma_api_404")) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "figma_discovery_unavailable"})
		return
	}
	writeFigmaError(w, err)
}

func figmaAccountHasCapability(item *db.ConnectedAccount, capability string) bool {
	return item != nil && item.Provider == "figma" && item.Status == "active" && item.RevokedAt == nil && containsString(item.Capabilities, capability)
}
