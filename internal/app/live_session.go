package app

import (
	"net/http"

	"github.com/misty-org/misty-server/internal/accounts"
	"github.com/misty-org/misty-server/internal/platform/security"
	"github.com/misty-org/misty-server/internal/platform/transport"
)

func (s *Server) liveSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := accounts.SessionUserID(r)
		sid := accounts.SessionID(r)
		if err != nil || user == "" || sid == "" {
			transport.WriteJSON(w, http.StatusUnauthorized, map[string]string{"code": "not_authenticated"})
			return
		}
		active, err := s.Database.AccountSessionActive(r.Context(), security.HashToken(sid), user)
		if err != nil {
			transport.WriteJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
			return
		}
		if !active {
			transport.WriteJSON(w, http.StatusUnauthorized, map[string]string{"code": "not_authenticated"})
			return
		}
		next(w, r)
	}
}
