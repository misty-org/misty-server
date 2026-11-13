package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/misty-org/misty-server/internal/platform/db"
)

// MyInviteCode answers GET /me/invite-code with the code of the person's invite link.
func MyInviteCode(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		code, err := database.InviteCode(r.Context(), userID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"code": code})
	}
}

// InviteByCode answers GET /invites/{code} with who an invite link is from. It needs no
// account, since whoever opens the link has none yet, and says nothing but the username.
func InviteByCode(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		code := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "code")))
		name, err := database.InviterName(r.Context(), code)
		switch {
		case errors.Is(err, db.ErrInviteCodeNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "invite_not_found"})
		case err != nil:
			http.Error(w, "internal error", http.StatusInternalServerError)
		default:
			writeJSON(w, http.StatusOK, map[string]string{"name": name})
		}
	}
}
