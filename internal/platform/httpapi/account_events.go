package api

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/misty-org/misty-server/internal/accounts"
	"github.com/misty-org/misty-server/internal/platform/security"
)

var accountEventSessionCheck = 10 * time.Minute

func accountEventRevalidation(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.75 + 0.5*rand.Float64()))
}

func (s *SpacesService) AccountEvents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unavailable", http.StatusInternalServerError)
			return
		}
		events, unsubscribe, err := s.database.SubscribeAccountEvents(r.Context(), userID)
		if err != nil {
			w.Header().Set("Retry-After", "30")
			http.Error(w, "event service unavailable", http.StatusServiceUnavailable)
			return
		}
		defer unsubscribe()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		keepalive := time.NewTicker(25 * time.Second)
		defer keepalive.Stop()
		session := ""
		if sid := accounts.SessionID(r); sid != "" {
			session = security.HashToken(sid)
		}
		revalidate := time.NewTimer(accountEventRevalidation(accountEventSessionCheck))
		defer revalidate.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-revalidate.C:
				if session == "" {
					return
				}
				active, err := s.database.AccountSessionActive(r.Context(), session, userID)
				if err == nil && !active {
					return
				}
				next := accountEventRevalidation(accountEventSessionCheck)
				if err != nil {
					next = accountEventRevalidation(time.Minute)
				}
				revalidate.Reset(next)
			case event := <-events:
				event.UserID = ""
				body, _ := json.Marshal(event)
				if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
					return
				}
				flusher.Flush()
			case <-keepalive.C:
				if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}

func TestingSetAccountEventSessionCheck(d time.Duration) (restore func()) {
	previous := accountEventSessionCheck
	accountEventSessionCheck = d
	return func() { accountEventSessionCheck = previous }
}
