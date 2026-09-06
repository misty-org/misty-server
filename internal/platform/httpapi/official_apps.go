package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/appcatalog"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// OfficialAppRelease exposes only a digest of public package metadata for release promotion.
func OfficialAppRelease(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"catalog_sha256": appcatalog.Digest(), "host_protocol_version": appcatalog.HostProtocolVersion})
}

func OfficialApps(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authenticatedUser(w, r, database); !ok {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"apps":                  appcatalog.All(),
			"host_protocol_version": appcatalog.HostProtocolVersion,
		})
	}
}

func OfficialApp(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authenticatedUser(w, r, database); !ok {
			return
		}
		item, ok := appcatalog.Find(chi.URLParam(r, "appID"))
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "app_not_found"})
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func MyOfficialApps(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		items, err := database.UserApps(r.Context(), userID)
		if err != nil {
			writeOfficialAppError(w, err)
			return
		}
		visible := items[:0]
		for _, item := range items {
			if _, exists := appcatalog.Find(item.AppID); exists {
				visible = append(visible, item)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"apps": visible})
	}
}

func MyOfficialApp(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		catalogApp, exists := appcatalog.Find(chi.URLParam(r, "appID"))
		if !exists {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "app_not_found"})
			return
		}
		switch r.Method {
		case http.MethodPut:
			var body struct {
				PermissionVersion int `json:"permission_version"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			if body.PermissionVersion != catalogApp.PermissionVersion {
				writeJSON(w, http.StatusConflict, map[string]string{
					"code": "app_permissions_changed", "message": "Review the app's current permissions before installing.",
				})
				return
			}
			item, err := database.InstallUserApp(r.Context(), userID, catalogApp.ID, catalogApp.Version, catalogApp.PermissionVersion, catalogApp.Scopes)
			if err != nil {
				writeOfficialAppError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
		case http.MethodPatch:
			var body struct {
				Pinned *bool `json:"pinned"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			if body.Pinned == nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
				return
			}
			item, err := database.SetUserAppPinned(r.Context(), userID, catalogApp.ID, *body.Pinned)
			if err != nil {
				writeOfficialAppError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
		case http.MethodDelete:
			item, err := database.UninstallUserApp(r.Context(), userID, catalogApp.ID, time.Now())
			if err != nil {
				writeOfficialAppError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// CreateOfficialAppSession remains available for a future catalog App that is
// intentionally isolated. Host-embedded Apps use the normal account request
// path and must never mint a parallel restricted credential.
func CreateOfficialAppSession(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		appID := strings.TrimSpace(strings.ToLower(chi.URLParam(r, "appID")))
		catalogApp, exists := appcatalog.Find(appID)
		if !exists {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "app_not_found"})
			return
		}
		if catalogApp.Desktop.Runtime == appcatalog.RuntimeEmbedded {
			writeJSON(w, http.StatusConflict, map[string]string{
				"code": "app_is_host_embedded", "message": "This app runs inside the trusted Misty Host.",
			})
			return
		}
		var body struct {
			SpaceID string `json:"space_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		token, err := security.GenerateSecureToken()
		if err != nil {
			writeOfficialAppError(w, err)
			return
		}
		session, err := database.CreateAppRuntimeSession(
			r.Context(), userID, catalogApp.ID, security.HashToken(token), body.SpaceID, db.AppRuntimeSessionTTL,
		)
		if err != nil {
			writeOfficialAppError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"token": token, "app_id": session.AppID, "space_id": session.SpaceID,
			"scopes": session.Scopes, "expires_at": session.ExpiresAt,
			"sdk_base_url": "/v1/app-runtime",
		})
	}
}

func appRuntimeSession(w http.ResponseWriter, r *http.Request, database *db.Database) (*db.AppRuntimeSession, bool) {
	token, ok := TestingBearerTokenFromRequest(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "app_session_required"})
		return nil, false
	}
	session, err := database.AppRuntimeSessionByToken(r.Context(), security.HashToken(token))
	if err != nil {
		writeOfficialAppError(w, err)
		return nil, false
	}
	if session == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "app_session_expired"})
		return nil, false
	}
	return session, true
}

func OfficialAppRuntimeSession(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := appRuntimeSession(w, r, database)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, session)
	}
}

func OfficialAppPersonalRecords(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := appRuntimeSession(w, r, database)
		if !ok {
			return
		}
		items, err := database.AppPersonalRecords(r.Context(), *session)
		if err != nil {
			writeOfficialAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"records": items})
	}
}

func OfficialAppPersonalRecord(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := appRuntimeSession(w, r, database)
		if !ok {
			return
		}
		key := chi.URLParam(r, "recordKey")
		switch r.Method {
		case http.MethodPut:
			var body struct {
				Data json.RawMessage `json:"data"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			item, err := database.PutAppPersonalRecord(r.Context(), *session, key, body.Data)
			if err != nil {
				writeOfficialAppError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
		case http.MethodDelete:
			deleted, err := database.DeleteAppPersonalRecord(r.Context(), *session, key)
			if err != nil {
				writeOfficialAppError(w, err)
				return
			}
			if !deleted {
				writeJSON(w, http.StatusNotFound, map[string]string{"code": "record_not_found"})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func writeOfficialAppError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrAppNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "app_not_installed"})
	case errors.Is(err, db.ErrAppNotInstalled):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "app_not_installed"})
	case errors.Is(err, db.ErrAppAlreadyPurging):
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "app_data_purging", "message": "This app's private data is already being permanently deleted.",
		})
	case errors.Is(err, db.ErrAppRuntimeForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "app_runtime_forbidden"})
	case errors.Is(err, db.ErrAppRecordInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_app_record"})
	case errors.Is(err, db.ErrSpaceInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error", "message": "The app request could not be completed."})
	}
}
