package api

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	"github.com/kannachi323/misty/server/internal/platform/entitlement"
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
		claims, ok := requireSelfHostProof(w, r)
		if !ok {
			return
		}
		user, err := database.CreateSelfHostBootstrapAdmin(r.Context(), body.Name, body.Username, body.Email, body.Password,
			security.HashToken(body.BootstrapToken), claims.Subject, time.Unix(claims.ExpiresAt, 0).UTC())
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
		claims, ok := requireSelfHostProof(w, r)
		if !ok {
			return
		}
		user, err := database.CreateSelfHostEnrolledUser(r.Context(), body.Name, body.Username, body.Email, body.Password,
			security.HashToken(body.Invitation), claims.Subject, time.Unix(claims.ExpiresAt, 0).UTC())
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

func RenewSelfHostEntitlement(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if InstanceConfigFromEnv().Deployment != "self_hosted" {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		userID, err := sessionUserID(r, database)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
			return
		}
		if userID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "not_authenticated"})
			return
		}
		claims, ok := requireSelfHostProof(w, r)
		if !ok {
			return
		}
		if err := database.RenewSelfHostEntitlement(r.Context(), userID, claims.Subject, time.Unix(claims.ExpiresAt, 0).UTC()); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "entitlement_subject_mismatch"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "eligible", "expires_at": time.Unix(claims.ExpiresAt, 0).UTC()})
	}
}

func SelfHostedEntitlementMiddleware(database *db.Database) func(http.Handler) http.Handler {
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
			if err != nil || access.Disabled || !access.EntitlementExpiresAt.After(time.Now().UTC()) {
				writeJSON(w, http.StatusPaymentRequired, map[string]any{
					"code":    "self_host_entitlement_required",
					"actions": []string{"retry_verification", "open_settings", "switch_hosted", "sign_out"},
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func SelfHostedFeatureGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if InstanceConfigFromEnv().Deployment != "self_hosted" {
			next.ServeHTTP(w, r)
			return
		}
		path := trimPublicAPIPrefix(r.URL.Path)
		blockedPrefix := []string{
			"/ai", "/agents", "/billing", "/cloud", "/integrations", "/misty",
			"/provider-callbacks", "/runs", "/waitlist", "/auth/forgot", "/auth/reset", "/auth/handoff",
		}
		for _, prefix := range blockedPrefix {
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				writeJSON(w, http.StatusNotImplemented, map[string]string{"code": "feature_unavailable_self_hosted"})
				return
			}
		}
		if strings.Contains(path, "/integrations/") || strings.Contains(path, "/provider-resources") || strings.Contains(path, "/agents/") {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"code": "feature_unavailable_self_hosted"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func acceptSelfHostLoginProof(w http.ResponseWriter, r *http.Request, database *db.Database, userID string) bool {
	if InstanceConfigFromEnv().Deployment != "self_hosted" {
		return true
	}
	claims, ok := requireSelfHostProof(w, r)
	if !ok {
		return false
	}
	if err := database.RenewSelfHostEntitlement(r.Context(), userID, claims.Subject, time.Unix(claims.ExpiresAt, 0).UTC()); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "entitlement_subject_mismatch"})
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

func requireSelfHostProof(w http.ResponseWriter, r *http.Request) (entitlement.Claims, bool) {
	proof := strings.TrimSpace(r.Header.Get(entitlementHeader))
	claims, err := entitlement.Verify(proof, selfHostEntitlementPublicKeys(), time.Now().UTC())
	if err != nil {
		writeJSON(w, http.StatusPaymentRequired, map[string]string{"code": "self_host_entitlement_required"})
		return entitlement.Claims{}, false
	}
	return claims, true
}

func selfHostEntitlementPublicKeys() map[string]ed25519.PublicKey {
	keys := make(map[string]ed25519.PublicKey, len(entitlement.BundledPublicKeys))
	for keyID, key := range entitlement.BundledPublicKeys {
		keys[keyID] = key
	}
	var configured map[string]string
	if json.Unmarshal([]byte(strings.TrimSpace(envconfig.Getenv("MISTY_SELF_HOST_ENTITLEMENT_PUBLIC_KEYS"))), &configured) == nil {
		for keyID, encoded := range configured {
			raw, err := base64.StdEncoding.DecodeString(encoded)
			if err == nil && len(raw) == ed25519.PublicKeySize && strings.TrimSpace(keyID) != "" {
				keys[keyID] = ed25519.PublicKey(raw)
			}
		}
	}
	return keys
}

func selfHostRecoveryPath(path string) bool {
	path = trimPublicAPIPrefix(path)
	switch path {
	case "/health", "/instance", "/login", "/logout", "/self-host/bootstrap", "/self-host/enroll", "/self-host/entitlement":
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
