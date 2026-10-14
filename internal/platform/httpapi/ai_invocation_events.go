package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// InvocationEvents streams an invocation's events as server-sent events,
// resuming after Last-Event-ID.
func (s *AIService) InvocationEvents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		id := strings.TrimSpace(chi.URLParam(r, "invocationID"))
		stored, err := s.database.AIInvocationByID(r.Context(), user, id)
		if err != nil || time.Now().After(stored.ExpiresAt) {
			http.Error(w, "invocation not found", http.StatusNotFound)
			return
		}
		if _, ok := w.(http.Flusher); !ok {
			http.Error(w, "streaming unavailable", http.StatusInternalServerError)
			return
		}
		// Preserve recovery for interrupted non-durable legacy runs.
		if !aiInvocationTerminal(stored.State) && stored.State != "queued" && stored.RuntimeRunID == "" && stored.AgentRunID == "" {
			if _, err := s.invocations.restoreDurable(r.Context(), *stored); err != nil {
				return
			}
			if err := s.invocations.fail(id, "Misty was interrupted before finishing. Please retry the request."); err != nil {
				return
			}
		}
		cursor, _ := strconv.ParseInt(strings.TrimSpace(r.Header.Get("Last-Event-ID")), 10, 64)
		cursor = max(cursor, 0)
		stream, release := s.streams.acquire(user, id, cursor)
		defer release()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("X-Accel-Buffering", "no")
		serveInvocationStream(r.Context(), w, stream, cursor, 15*time.Second)
	}
}

// Each SSE write is bounded, but a healthy event stream must outlive the JSON
// middleware's original 60-second deadline. Heartbeats refresh this deadline.
func writeAIInvocationSSE(w http.ResponseWriter, frame string) error {
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	if _, err := fmt.Fprint(w, frame); err != nil {
		return err
	}
	return controller.Flush()
}
