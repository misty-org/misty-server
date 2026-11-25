package api

import (
	"database/sql"
	"errors"
	"io"
	"net/http"

	"github.com/misty-org/misty-server/internal/avatars"
	"github.com/misty-org/misty-server/internal/platform/db"
)

func readAvatarDecoration(w http.ResponseWriter, r *http.Request) (avatars.Decoration, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, avatars.MaxDecorationBytes+1)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		_ = rejectInvalidJSON(w)
		return avatars.Decoration{}, false
	}
	decoration, err := avatars.Parse(data)
	if err != nil {
		_ = rejectInvalidJSON(w)
		return avatars.Decoration{}, false
	}
	return decoration, true
}

func UserAvatarDecoration(database *db.Database, realtime *RealtimeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		decoration, ok := readAvatarDecoration(w, r)
		if !ok {
			return
		}
		saved, err := database.SetUserAvatarDecoration(userID, decoration)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		_ = database.AnnounceMemberAvatarDecoration(r.Context(), userID, saved)
		go realtime.PublishProfile(userID, map[string]any{"avatar_decoration": saved})
		writeJSON(w, http.StatusOK, map[string]any{"avatar_decoration": saved})
	}
}
