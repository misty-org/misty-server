package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *AIService) ProactiveEvent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		surfaceID := strings.TrimSpace(chi.URLParam(r, "surfaceID"))
		if !aiSurfaceIDs[surfaceID] {
			http.Error(w, "invalid surface", http.StatusBadRequest)
			return
		}
		var body struct {
			Event         string `json:"event"`
			SnoozeMinutes int    `json:"snooze_minutes"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		preference, err := s.database.RecordAIProactiveEvent(
			r.Context(), userID, surfaceID, body.Event, body.SnoozeMinutes, time.Now().UTC(),
		)
		if errors.Is(err, db.ErrSpaceConflict) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"code": "proactive_cooldown", "message": "This suggestion is snoozed or cooling down.",
			})
			return
		}
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"preference": preference})
	}
}
