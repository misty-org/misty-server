package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

const TestingSessionCookieName = "misty_session"

func sessionUserID(r *http.Request, database *db.Database) (string, error) {
	token, ok := sessionTokenFromRequest(r)
	if !ok {
		return "", nil
	}
	tokenHash := security.HashToken(token)
	return database.GetSessionUserID(tokenHash)
}

func sessionTokenFromRequest(r *http.Request) (string, bool) {
	if token, ok := TestingBearerTokenFromRequest(r); ok {
		return token, true
	}
	cookie, err := r.Cookie(TestingSessionCookieName)
	if err != nil {
		return "", false
	}
	token := strings.TrimSpace(cookie.Value)
	return token, token != ""
}

func TestingBearerTokenFromRequest(r *http.Request) (string, bool) {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	scheme, token, ok := strings.Cut(authHeader, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

func GetMe(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, database)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}

		user, err := database.GetUserByID(userID)
		if err != nil || user == nil {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}

		license, err := database.GetLicenseByUserID(userID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if license == nil {
			http.Error(w, "license not found", http.StatusInternalServerError)
			return
		}

		subscription, err := database.GetStripeSubscriptionByUserID(userID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		billingKind := "free"
		if license.Status == db.LicenseStatusTrialing {
			billingKind = "trial"
		} else if subscription != nil && db.SubscriptionAllowsPaidAccess(subscription.Status) {
			billingKind = "subscription"
		} else if license.LegacyTier != nil {
			billingKind = "lifetime"
		}
		billingSummary := map[string]any{"kind": billingKind, "interval": nil, "subscription_status": nil,
			"current_period_end": nil, "cancel_at_period_end": false, "customer_portal_available": false}
		if subscription != nil {
			billingSummary["interval"] = subscription.BillingInterval
			billingSummary["subscription_status"] = subscription.Status
			billingSummary["current_period_end"] = subscription.CurrentPeriodEnd
			billingSummary["cancel_at_period_end"] = subscription.CancelAtPeriodEnd
			billingSummary["customer_portal_available"] = subscription.StripeCustomerID != ""
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"id":               user.ID,
			"name":             user.Name,
			"username":         user.Username,
			"email":            user.Email,
			"avatar_version":   user.AvatarVersion,
			"created_at":       user.CreatedAt,
			"tier":             string(db.NormalizePlan(license.Tier)),
			"status":           license.Status,
			"allows_use":       TestingLicenseAllowsUse(license),
			"expires_at":       license.ExpiresAt,
			"trial_started_at": license.TrialStartedAt,
			"license_device":   license.LicenseDevice,
			"billing":          billingSummary,
		})
	}
}

const maxAvatarPNGBytes = 5 << 20

// avatarObjectKey is where a user's avatar PNG lives in the shared object store.
func avatarObjectKey(userID string) string { return "avatars/" + userID }

func UserAvatar(database *db.Database, store LibraryObjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, database)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}

		switch r.Method {
		case http.MethodGet:
			version, err := database.GetUserAvatarVersion(userID)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if version == 0 {
				http.Error(w, "avatar not found", http.StatusNotFound)
				return
			}
			serveAvatarObject(w, r, store, userID, version)
		case http.MethodPut:
			data, ok := TestingReadAvatarPNG(w, r)
			if !ok {
				return
			}
			if store == nil {
				http.Error(w, "avatar storage unavailable", http.StatusServiceUnavailable)
				return
			}
			sum := sha256.Sum256(data)
			metadata := LibraryObjectMetadata{
				ByteSize: int64(len(data)),
				SHA256:   hex.EncodeToString(sum[:]),
				MIMEType: "image/png",
			}
			if err := store.Put(r.Context(), avatarObjectKey(userID), bytes.NewReader(data), metadata); err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			version, err := database.BumpUserAvatarVersion(userID)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"avatar_version": version})
		default:
			w.Header().Set("Allow", "GET, PUT")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// serveAvatarObject streams a user's avatar PNG from the object store (R2).
// The version supplies the ETag; a missing object is a 404.
func serveAvatarObject(
	w http.ResponseWriter,
	r *http.Request,
	store LibraryObjectStore,
	userID string,
	version int64,
) {
	if store == nil {
		http.Error(w, "avatar not found", http.StatusNotFound)
		return
	}
	reader, _, err := store.Open(r.Context(), avatarObjectKey(userID))
	if err != nil {
		if errors.Is(err, ErrLibraryObjectNotFound) {
			http.Error(w, "avatar not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer reader.Close()
	setAvatarHeaders(w, version)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

func TestingReadAvatarPNG(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAvatarPNGBytes+1)
	data, err := io.ReadAll(r.Body)
	if err != nil || len(data) == 0 || len(data) > maxAvatarPNGBytes {
		http.Error(w, "PNG must be 5 MB or smaller", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 {
		http.Error(w, "valid PNG required", http.StatusBadRequest)
		return nil, false
	}
	return data, true
}

func setAvatarHeaders(w http.ResponseWriter, version int64) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("ETag", `"avatar-`+strconv.FormatInt(version, 10)+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
}
