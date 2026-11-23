package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/misty-org/misty-server/internal/platform/db"
)

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
			if err := s.database.RequireSpacePermission(r.Context(), userID, spaceID, db.PermissionSpaceRename); err != nil {
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
