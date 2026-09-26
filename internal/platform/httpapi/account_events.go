package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// AccountEvents streams committed invalidations, not private record contents.
// Every reconnect starts with reset: snapshots remain the durable source of truth.
func (s *AIService) AccountEvents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if db.AppAuthorityFromContext(r.Context()) != nil {
			http.Error(w, "Host-only account events", http.StatusForbidden)
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
		keepalive := time.NewTicker(25 * time.Second)
		defer keepalive.Stop()
		// Periodic reconnection revalidates the authenticated session.
		expiry := time.NewTimer(10 * time.Minute)
		defer expiry.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-expiry.C:
				return
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
