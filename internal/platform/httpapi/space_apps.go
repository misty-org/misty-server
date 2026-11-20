package api

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// maxAppStorageBodyBytes is a stored value at its longest (64 KB) plus its envelope.
const maxAppStorageBodyBytes = 70 << 10

// SpaceApps lists the apps a Space has installed.
func (s *SpacesService) SpaceApps() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		apps, err := s.database.SpaceApps(r.Context(), userID, chi.URLParam(r, "spaceID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"apps": apps})
	}
}

// SpaceApp installs an app (PUT, which also moves an installed one to a new version) or removes it.
func (s *SpacesService) SpaceApp() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, appID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "appID")
		switch r.Method {
		case http.MethodPut:
			var body struct {
				Version     string   `json:"version"`
				Permissions []string `json:"permissions"`
			}
			if decodeJSONUpTo(w, r, &body, 8<<10) != nil {
				return
			}
			app, err := s.database.InstallSpaceApp(r.Context(), userID, spaceID, appID, body.Version, body.Permissions)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, app)
		case http.MethodDelete:
			if err := s.database.RemoveSpaceApp(r.Context(), userID, spaceID, appID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// SpaceAppStorage lists an app's stored keys, those starting with ?prefix= when given.
func (s *SpacesService) SpaceAppStorage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		values, err := s.database.SpaceAppValues(r.Context(), userID, chi.URLParam(r, "spaceID"),
			chi.URLParam(r, "appID"), r.URL.Query().Get("prefix"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"values": values})
	}
}

// SpaceAppStorageKey reads, writes or removes one key of an app's storage.
func (s *SpacesService) SpaceAppStorageKey() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		// chi hands back the path segment as sent, so a key with a space or a slash arrives escaped.
		key, err := url.PathUnescape(chi.URLParam(r, "key"))
		if err != nil {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		spaceID, appID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "appID")
		switch r.Method {
		case http.MethodGet:
			value, err := s.database.SpaceAppValueGet(r.Context(), userID, spaceID, appID, key)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, value)
		case http.MethodPut:
			var body struct {
				Value json.RawMessage `json:"value"`
			}
			if decodeJSONUpTo(w, r, &body, maxAppStorageBodyBytes) != nil {
				return
			}
			value, err := s.database.SpaceAppValueSet(r.Context(), userID, spaceID, appID, key, body.Value)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, value)
		case http.MethodDelete:
			if err := s.database.SpaceAppValueRemove(r.Context(), userID, spaceID, appID, key); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}
