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

const (
	maxBannerPNGBytes = 5 << 20
	minBannerWidth    = 300
	maxBannerWidth    = 3000
	minBannerHeight   = 100
	maxBannerHeight   = 1000
)

func bannerObjectKey(userID string) string { return "banners/" + userID }

func newBannerObjectKey(userID string) (string, error) {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	return "banners/" + userID + "-" + hex.EncodeToString(suffix), nil
}

func UserBanner(database *db.Database, store LibraryObjectStore) http.HandlerFunc {
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
			banner, err := database.GetUserBannerReference(userID)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if banner.Version == 0 {
				http.Error(w, "banner not found", http.StatusNotFound)
				return
			}
			serveBannerObject(w, r, store, banner.ObjectKey, banner.Version)
		case http.MethodPut:
			data, ok := ReadBannerPNG(w, r)
			if !ok {
				return
			}
			if store == nil {
				http.Error(w, "banner storage unavailable", http.StatusServiceUnavailable)
				return
			}
			sum := sha256.Sum256(data)
			metadata := LibraryObjectMetadata{
				ByteSize: int64(len(data)),
				SHA256:   hex.EncodeToString(sum[:]),
				MIMEType: "image/png",
			}
			objectKey, err := newBannerObjectKey(userID)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if err := store.Put(r.Context(), objectKey, bytes.NewReader(data), metadata); err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			version, err := database.BumpUserBannerVersion(userID, objectKey)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"banner_version": version})
		case http.MethodDelete:
			if err := database.RemoveUserBanner(userID); err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"banner_version": 0})
		default:
			w.Header().Set("Allow", "GET, PUT, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func serveBannerObject(
	w http.ResponseWriter,
	r *http.Request,
	store LibraryObjectStore,
	objectKey string,
	version int64,
) {
	if store == nil {
		http.Error(w, "banner not found", http.StatusNotFound)
		return
	}
	reader, _, err := store.Open(r.Context(), objectKey)
	if err != nil {
		if errors.Is(err, ErrLibraryObjectNotFound) {
			http.Error(w, "banner not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer reader.Close()
	setBannerHeaders(w, version)
	if r.Header.Get("If-None-Match") == w.Header().Get("ETag") {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

func ReadBannerPNG(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBannerPNGBytes+1)
	data, err := io.ReadAll(r.Body)
	if err != nil || len(data) == 0 || len(data) > maxBannerPNGBytes {
		http.Error(w, "PNG must be 5 MB or smaller", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		http.Error(w, "valid PNG required", http.StatusBadRequest)
		return nil, false
	}
	if !bannerShapeAllowed(config.Width, config.Height) {
		http.Error(w, "PNG banner must be 3:1, up to 3000 × 1000", http.StatusBadRequest)
		return nil, false
	}
	return data, true
}

func bannerShapeAllowed(width, height int) bool {
	if width < minBannerWidth || width > maxBannerWidth || height < minBannerHeight || height > maxBannerHeight {
		return false
	}
	target := 3 * height
	diff := width - target
	if diff < 0 {
		diff = -diff
	}
	return diff*100 <= target
}

func setBannerHeaders(w http.ResponseWriter, version int64) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", `"banner-`+strconv.FormatInt(version, 10)+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
}
