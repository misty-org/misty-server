package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func ClosedSelfHostRegistration() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "self_host_registration_closed"})
	}
}

func SelfHostBootstrap(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if InstanceConfigFromEnv().Deployment != "self_hosted" {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		var body selfHostAccountRequest
		if decodeJSON(w, r, &body) != nil || !body.valid() || strings.TrimSpace(body.BootstrapToken) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
			return
		}
		user, err := database.CreateSelfHostBootstrapAdmin(r.Context(), body.Name, body.Username, body.Email, body.Password,
			security.HashToken(body.BootstrapToken), "local:"+uuid.NewString(), time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC))
		if err != nil {
			writeSelfHostAccountError(w, err)
			return
		}
		writeAuthSession(w, r, database, user, http.StatusCreated)
	}
}

func SelfHostEnroll(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if InstanceConfigFromEnv().Deployment != "self_hosted" {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		var body selfHostAccountRequest
		if decodeJSON(w, r, &body) != nil || !body.valid() || strings.TrimSpace(body.Invitation) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
			return
		}
		user, err := database.CreateSelfHostEnrolledUser(r.Context(), body.Name, body.Username, body.Email, body.Password,
			security.HashToken(body.Invitation), "local:"+uuid.NewString(), time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC))
		if err != nil {
			writeSelfHostAccountError(w, err)
			return
		}
		writeAuthSession(w, r, database, user, http.StatusCreated)
	}
}

func SelfHostInvitation(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if InstanceConfigFromEnv().Deployment != "self_hosted" {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		if r.Method == http.MethodDelete {
			invitationID := chi.URLParam(r, "invitationID")
			if err := database.RevokeSelfHostInvitation(r.Context(), userID, invitationID); err != nil {
				if errors.Is(err, db.ErrSelfHostNotAdmin) {
					writeJSON(w, http.StatusForbidden, map[string]string{"code": "admin_required"})
					return
				}
				if errors.Is(err, db.ErrSelfHostInviteInvalid) {
					writeJSON(w, http.StatusNotFound, map[string]string{"code": "enrollment_invitation_not_found"})
					return
				}
				writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		token, err := security.GenerateSecureToken()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
			return
		}
		expiresAt := time.Now().UTC().Add(7 * 24 * time.Hour)
		invitationID := "enrollment_" + uuid.NewString()
		if err := database.CreateSelfHostInvitation(r.Context(), userID, invitationID, security.HashToken(token), expiresAt); err != nil {
			if errors.Is(err, db.ErrSelfHostNotAdmin) {
				writeJSON(w, http.StatusForbidden, map[string]string{"code": "admin_required"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": invitationID, "invitation": token, "expires_at": expiresAt})
	}
}

func SelfHostedAccountMiddleware(database *db.Database) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if InstanceConfigFromEnv().Deployment != "self_hosted" || selfHostRecoveryPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			userID, err := sessionUserID(r, database)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
				return
			}
			if userID == "" {
				next.ServeHTTP(w, r)
				return
			}
			access, err := database.SelfHostAccountAccess(r.Context(), userID)
			if err != nil || access.Disabled {
				writeJSON(w, http.StatusForbidden, map[string]any{
					"code":    "self_host_account_disabled",
					"actions": []string{"open_settings", "switch_hosted", "sign_out"},
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func acceptSelfHostLogin(w http.ResponseWriter, r *http.Request, database *db.Database, userID string) bool {
	if InstanceConfigFromEnv().Deployment != "self_hosted" {
		return true
	}
	access, err := database.SelfHostAccountAccess(r.Context(), userID)
	if err != nil || access.Disabled {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "self_host_account_disabled"})
		return false
	}
	return true
}

type selfHostAccountRequest struct {
	Name           string `json:"name"`
	Username       string `json:"username"`
	Email          string `json:"email"`
	Password       string `json:"password"`
	BootstrapToken string `json:"bootstrap_token"`
	Invitation     string `json:"invitation"`
}

func (body selfHostAccountRequest) valid() bool {
	return strings.TrimSpace(body.Name) != "" && strings.TrimSpace(body.Username) != "" &&
		strings.TrimSpace(body.Email) != "" && len(body.Password) >= 8
}

func selfHostRecoveryPath(path string) bool {
	path = trimPublicAPIPrefix(path)
	switch path {
	case "/health", "/instance", "/login", "/logout", "/self-host/bootstrap", "/self-host/enroll":
		return true
	default:
		return false
	}
}

func trimPublicAPIPrefix(path string) string {
	for _, prefix := range []string{"/api", "/v1"} {
		if path == prefix {
			return "/"
		}
		if strings.HasPrefix(path, prefix+"/") {
			return strings.TrimPrefix(path, prefix)
		}
	}
	return path
}

func writeSelfHostAccountError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrSelfHostBootstrapInvalid):
		writeJSON(w, http.StatusGone, map[string]string{"code": "bootstrap_token_invalid"})
	case errors.Is(err, db.ErrSelfHostInviteInvalid):
		writeJSON(w, http.StatusGone, map[string]string{"code": "enrollment_invitation_invalid"})
	case errors.Is(err, db.ErrSelfHostSubjectBound):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "entitlement_subject_already_enrolled"})
	case errors.Is(err, db.ErrInvalidUsername):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_username"})
	case errors.Is(err, db.ErrUsernameTaken) || err.Error() == "email already registered":
		writeJSON(w, http.StatusConflict, map[string]string{"code": "account_already_exists"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
	}
}
