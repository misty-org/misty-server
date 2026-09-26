package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/kannachi323/misty/server/internal/accounts"
	"image/png"
	"io"
	"net/http"
	"strconv"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const TestingSessionCookieName = "misty_session"

func sessionUserID(r *http.Request, _ *db.Database) (string, error) { return accounts.SessionUserID(r) }
func TestingBearerTokenFromRequest(r *http.Request) (string, bool) {
	return accounts.BearerTokenFromRequest(r)
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

		summary, billingErr := accountBillingSummary(r.Context(), database, userID)
		if billingErr != nil {
			summary = map[string]any{"tier": "basic", "status": "active", "trial_eligible": false, "billing": map[string]any{"kind": "unavailable", "customer_portal_available": false}}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"id":               user.ID,
			"name":             user.Name,
			"username":         user.Username,
			"email":            user.Email,
			"avatar_version":   user.AvatarVersion,
			"created_at":       user.CreatedAt,
			"tier":             summary["tier"],
			"status":           summary["status"],
			"allows_use":       true,
			"expires_at":       summary["expires_at"],
			"trial_started_at": summary["trial_started_at"],
			"trial_eligible":   summary["trial_eligible"],
			"license_device":   "",
			"billing":          summary["billing"],
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
			avatar, err := database.GetUserAvatarReference(userID)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if avatar.Version == 0 {
				http.Error(w, "avatar not found", http.StatusNotFound)
				return
			}
			serveAvatarObject(w, r, store, avatar.ObjectKey, avatar.Version)
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
	objectKey string,
	version int64,
) {
	if store == nil {
		http.Error(w, "avatar not found", http.StatusNotFound)
		return
	}
	reader, _, err := store.Open(r.Context(), objectKey)
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
