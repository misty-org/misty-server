package api

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func decodeCapabilityRequest(w http.ResponseWriter, r *http.Request, out any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 3<<20))
	if err != nil || cap.Decode(raw, out) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_capability_request"})
		return false
	}
	return true
}
func trustedSDKUser(w http.ResponseWriter, r *http.Request, database *db.Database) (string, bool) {
	userID, ok := authenticatedUser(w, r, database)
	if !ok {
		return "", false
	}
	if db.AppAuthorityFromContext(r.Context()) != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "trusted_user_control_required"})
		return "", false
	}
	return userID, true
}

// Disable new installations and registrations without removing lifecycle or
// discovery records needed for cleanup and future pinned-run reconciliation.
func sdkProviderAdmissionEnabled(w http.ResponseWriter) bool {
	if envconfig.Getenv("MISTY_SDK_PROVIDERS_ENABLED") == "true" {
		return true
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "sdk_provider_admission_disabled"})
	return false
}

func InstallSDKApp(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := trustedSDKUser(w, r, database)
		if !ok {
			return
		}
		if !sdkProviderAdmissionEnabled(w) {
			return
		}
		var body struct {
			Manifest       cap.SignedManifest `json:"manifest"`
			ReviewedDigest string             `json:"reviewedDigest"`
		}
		if !decodeCapabilityRequest(w, r, &body) {
			return
		}
		installed, err := database.InstallVerifiedSDKApp(r.Context(), userID, body.Manifest, body.ReviewedDigest)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, installed)
	}
}
func CreateSDKAppSession(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := trustedSDKUser(w, r, database)
		if !ok {
			return
		}
		if !sdkProviderAdmissionEnabled(w) {
			return
		}
		appID := chi.URLParam(r, "appID")
		installed, err := database.IsVerifiedSDKAppInstalled(r.Context(), userID, appID)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		if !installed {
			writeSDKError(w, db.ErrAppNotFound)
			return
		}
		var body struct {
			SpaceID string `json:"spaceId"`
		}
		if !decodeCapabilityRequest(w, r, &body) {
			return
		}
		token, err := security.GenerateSecureToken()
		if err != nil {
			writeSDKError(w, err)
			return
		}
		session, err := database.CreateAppRuntimeSession(r.Context(), userID, appID, security.HashToken(token), body.SpaceID, db.AppRuntimeSessionTTL)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, map[string]any{"token": token, "app_id": session.AppID, "space_id": session.SpaceID, "scopes": session.Scopes, "expires_at": session.ExpiresAt, "sdk_base_url": "/v1/app-runtime"})
	}
}
func UninstallSDKApp(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := trustedSDKUser(w, r, database)
		if !ok {
			return
		}
		appID := chi.URLParam(r, "appID")
		installed, err := database.IsVerifiedSDKAppInstalled(r.Context(), userID, appID)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		if !installed {
			writeSDKError(w, db.ErrAppNotFound)
			return
		}
		result, err := database.UninstallUserApp(r.Context(), userID, appID, time.Now())
		if err != nil {
			writeSDKError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}
func RegisterSDKProvider(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		if !sdkProviderAdmissionEnabled(w) {
			return
		}
		var body struct {
			ManifestDigest string       `json:"manifestDigest"`
			Provider       cap.Provider `json:"provider"`
		}
		if !decodeCapabilityRequest(w, r, &body) {
			return
		}
		availability, err := database.RegisterSDKProvider(r.Context(), userID, body.ManifestDigest, body.Provider)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"provider": body.Provider, "availability": availability})
	}
}
func SDKProviderLifecycle(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		providerID, err := url.PathUnescape(chi.URLParam(r, "providerID"))
		if err != nil {
			writeSDKError(w, cap.ErrInvalid)
			return
		}
		switch r.Method {
		case http.MethodDelete:
			err = database.UnregisterSDKProvider(r.Context(), userID, providerID)
		case http.MethodPut:
			var state cap.Availability
			if !decodeCapabilityRequest(w, r, &state) {
				return
			}
			err = database.ReportSDKProviderAvailability(r.Context(), userID, providerID, state)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err != nil {
			writeSDKError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func DiscoverSDKProviders(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		var body db.SDKProviderDiscovery
		if !decodeCapabilityRequest(w, r, &body) {
			return
		}
		page, err := database.DiscoverSDKProviders(r.Context(), userID, body)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	}
}
func writeSDKError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrSDKTargetClarification):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "target_clarification_required", "message": err.Error()})
	case errors.Is(err, db.ErrSpaceNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "sdk_request_not_found"})
	case errors.Is(err, db.ErrSpaceConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "sdk_request_conflict"})
	case errors.Is(err, cap.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_capability_declaration"})
	case errors.Is(err, db.ErrSDKVersionConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "sdk_version_conflict", "message": "Publish a new version for changed app or capability content."})
	case errors.Is(err, db.ErrSDKPublisherChanged):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "sdk_publisher_changed", "message": "This signing key does not match the installed app."})
	case errors.Is(err, db.ErrSpaceForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "target_scope_forbidden"})
	case errors.Is(err, db.ErrSpaceLimit):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "target_selection_required", "message": "Choose an explicit target to narrow these results."})
	case errors.Is(err, db.ErrSDKProviderUnavailable):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "sdk_provider_unavailable"})
	default:
		writeOfficialAppError(w, err)
	}
}
