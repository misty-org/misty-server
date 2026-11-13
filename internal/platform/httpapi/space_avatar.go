package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/misty-org/misty-server/internal/platform/db"
)

// SpaceAvatar serves a Space's picture to its members, and lets its owner upload a new one (PUT,
// a PNG of at most 5 MB) or remove it (DELETE), after which the app draws the Space's initials.
func (s *SpacesService) SpaceAvatar() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			version, err := s.database.SpaceAvatarVersion(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			if version == 0 {
				writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
				return
			}
			serveAvatarObject(w, r, s.avatarStore, db.SpaceAvatarObjectKey(spaceID), version)
		case http.MethodPut:
			// Only the owner changes it; checked before anything is read or stored.
			if err := s.database.RequireSpaceOwner(r.Context(), userID, spaceID); err != nil {
				writeSpaceError(w, err)
				return
			}
			data, ok := ReadAvatarPNG(w, r)
			if !ok {
				return
			}
			if s.avatarStore == nil {
				http.Error(w, "avatar storage unavailable", http.StatusServiceUnavailable)
				return
			}
			sum := sha256.Sum256(data)
			metadata := LibraryObjectMetadata{
				ByteSize: int64(len(data)),
				SHA256:   hex.EncodeToString(sum[:]),
				MIMEType: "image/png",
			}
			if err := s.avatarStore.Put(r.Context(), db.SpaceAvatarObjectKey(spaceID), bytes.NewReader(data), metadata); err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			version, err := s.database.SetSpaceAvatar(r.Context(), userID, spaceID, true)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"avatar_version": version})
		case http.MethodDelete:
			version, err := s.database.SetSpaceAvatar(r.Context(), userID, spaceID, false)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			// A removed picture doesn't stay in storage. Members already see none; a failed delete
			// leaves only the bytes, which the Space's purge removes with the rest.
			if s.avatarStore != nil {
				_ = s.avatarStore.Delete(r.Context(), db.SpaceAvatarObjectKey(spaceID))
			}
			writeJSON(w, http.StatusOK, map[string]any{"avatar_version": version})
		default:
			w.Header().Set("Allow", "GET, PUT, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}
