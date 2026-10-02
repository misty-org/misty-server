package api

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/kannachi323/misty/server/internal/accounts"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// accountEventSessionCheck bounds how long a stream outlives a revoked session.
var accountEventSessionCheck = 10 * time.Minute

// accountEventRevalidation spreads checks across ±25% so streams opened
// together do not revalidate together.
func accountEventRevalidation(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.75 + 0.5*rand.Float64()))
}

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
		// Transport keepalive for proxies; it reads nothing.
		keepalive := time.NewTicker(25 * time.Second)
		defer keepalive.Stop()
		// The stream outlives the access token that opened it. It confirms in
		// band that the account session is still active rather than ending the
		// stream, whose reconnect would reload every account snapshot.
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
					return // No session to check: reconnect to re-authenticate.
				}
				active, err := s.database.AccountSessionActive(r.Context(), session, userID)
				if err == nil && !active {
					return
				}
				next := accountEventRevalidation(accountEventSessionCheck)
				if err != nil {
					// A database outage must not end every stream at once.
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

// TestingSetAccountEventSessionCheck shortens session revalidation for tests.
func TestingSetAccountEventSessionCheck(d time.Duration) (restore func()) {
	previous := accountEventSessionCheck
	accountEventSessionCheck = d
	return func() { accountEventSessionCheck = previous }
}
