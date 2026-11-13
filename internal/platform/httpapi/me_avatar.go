package api

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image/png"
	"io"
	"net/http"
	"strconv"

	"github.com/misty-org/misty-server/internal/platform/db"
)

const maxAvatarPNGBytes = 5 << 20

// avatarObjectKey is where a user's avatar PNG lived before each upload had its own key; account
// deletion still clears it for pictures uploaded then.
func avatarObjectKey(userID string) string { return "avatars/" + userID }

// newAvatarObjectKey names one upload's object: the person's id and a random suffix, flat, as the
// store's keys are.
func newAvatarObjectKey(userID string) (string, error) {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	return "avatars/" + userID + "-" + hex.EncodeToString(suffix), nil
}

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
			data, ok := ReadAvatarPNG(w, r)
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
			objectKey, err := newAvatarObjectKey(userID)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if err := store.Put(r.Context(), objectKey, bytes.NewReader(data), metadata); err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			version, err := database.BumpUserAvatarVersion(userID, objectKey)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			// The person's Spaces show the new picture without a reload; a failure here only
			// delays that until their next load.
			_ = database.AnnounceMemberAvatar(r.Context(), userID, version)
			writeJSON(w, http.StatusOK, map[string]any{"avatar_version": version})
		case http.MethodDelete:
			if err := database.RemoveUserAvatar(userID); err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			_ = database.AnnounceMemberAvatar(r.Context(), userID, 0)
			writeJSON(w, http.StatusOK, map[string]any{"avatar_version": 0})
		default:
			w.Header().Set("Allow", "GET, PUT, DELETE")
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
	// The version is the ETag, so an app that has this picture is told so without the bytes.
	if r.Header.Get("If-None-Match") == w.Header().Get("ETag") {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

func ReadAvatarPNG(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
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
	// Checked every time: a picture's URL stays the same across uploads, so a cache that kept it
	// for minutes would show the old one after a change.
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", `"avatar-`+strconv.FormatInt(version, 10)+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
}
