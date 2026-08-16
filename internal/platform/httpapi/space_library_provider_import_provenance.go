package api

import (
	"net/http"
	"regexp"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
)

var libraryProviderIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

// ProviderImportProvenance finalizes the source metadata for a provider file
// after its device-local download and normal Library upload have completed.
// Provider credentials and provider file contents never enter this request.
func (s *SpaceLibraryService) ProviderImportProvenance() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Provider         string `json:"provider"`
			RemoteName       string `json:"remote_name"`
			RemotePath       string `json:"remote_path"`
			ConnectionID     string `json:"connection_id"`
			ConnectionSource string `json:"connection_source"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		body.Provider = strings.ToLower(strings.TrimSpace(body.Provider))
		body.RemoteName = strings.TrimSpace(body.RemoteName)
		body.RemotePath = strings.TrimSpace(body.RemotePath)
		body.ConnectionID = strings.TrimSpace(body.ConnectionID)
		body.ConnectionSource = strings.TrimSpace(body.ConnectionSource)
		if !libraryProviderIdentifier.MatchString(body.Provider) || body.RemoteName == "" || len([]rune(body.RemoteName)) > 128 ||
			body.RemotePath == "" || len([]rune(body.RemotePath)) > 2048 {
			writeLibraryError(w, db.ErrLibraryInvalid)
			return
		}
		if body.ConnectionID != "" {
			connection, err := s.database.CloudConnection(r.Context(), userID, body.ConnectionID)
			if err != nil || connection.Name != body.RemoteName || connection.Provider != body.Provider {
				writeLibraryError(w, db.ErrLibraryForbidden)
				return
			}
			body.ConnectionSource = "legacy_cloud"
			if connection.ConnectedAccountID != "" {
				body.ConnectionSource = "connected_account"
			}
		} else {
			body.ConnectionSource = "device_remote"
		}
		item, err := s.database.SetLibraryImportProvenance(r.Context(), userID,
			chi.URLParam(r, "spaceID"), chi.URLParam(r, "itemID"), map[string]any{
				"provider": body.Provider, "remote_name": body.RemoteName,
				"remote_path": body.RemotePath, "connection_id": body.ConnectionID,
				"connection_source": body.ConnectionSource,
			})
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}
