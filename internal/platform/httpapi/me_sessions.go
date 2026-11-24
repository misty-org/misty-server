package api

import (
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/accounts"
	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
)

const sessionDeviceNameHeader = "X-Misty-Device-Name"

func sessionNameFor(r *http.Request) string {
	return accounts.AppSessionName(r.Header.Get(sessionDeviceNameHeader))
}

func currentSessionHash(r *http.Request) string {
	if sid := accounts.SessionID(r); sid != "" {
		return security.HashToken(sid)
	}
	return ""
}

func ListSessions(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		sessions, err := database.AccountSessions(r.Context(), userID, currentSessionHash(r))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		for index := range sessions {
			sessions[index].Name = accounts.DisplaySessionName(sessions[index].Name)
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
	}
}

func SignOutSession(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		sessionID := chi.URLParam(r, "sessionID")
		found, err := database.SignOutAccountSession(r.Context(), userID, sessionID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		if current := currentSessionHash(r); current != "" && security.SessionPublicIDFromHash(current) == sessionID {
			clearAuthCookies(w, r)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func signalSessionEnded(r *http.Request, database *db.Database, sessionID string) {
	if err := database.SignalCollabSessionEnded(r.Context(), security.SessionPublicID(sessionID)); err != nil {
		log.Printf("signal ended session: %v", err)
	}
}
