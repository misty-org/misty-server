package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

const maxAgentThreadBody = 4 << 20

func (s *SpacesService) AgentThreads() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		threads, err := s.database.AgentThreads(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "agentID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"threads": threads})
	}
}

func (s *SpacesService) AgentThread() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, threadID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "threadID")
		if r.Method == http.MethodDelete {
			if err := s.database.DeleteAgentThread(r.Context(), userID, spaceID, threadID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body struct {
			AgentID       string  `json:"agent_id"`
			Title         string  `json:"title"`
			State         string  `json:"state"`
			ContinuedFrom *string `json:"continued_from"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			return
		}
		thread, err := s.database.PutAgentThread(r.Context(), db.AgentThreadInput{
			UserID:        userID,
			SpaceID:       spaceID,
			ThreadID:      threadID,
			AgentID:       body.AgentID,
			Title:         body.Title,
			State:         body.State,
			ContinuedFrom: body.ContinuedFrom,
		})
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, thread)
	}
}

func (s *SpacesService) AgentThreadEntries() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, threadID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "threadID")
		if r.Method == http.MethodPost {
			var body struct {
				Entries []db.AgentThreadEntryInput `json:"entries"`
			}
			if err := decodeJSONUpTo(w, r, &body, maxAgentThreadBody); err != nil {
				return
			}
			thread, err := s.database.PutAgentThreadEntries(r.Context(), userID, spaceID, threadID, body.Entries)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, thread)
			return
		}
		after := int64(0)
		if raw := r.URL.Query().Get("after"); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || parsed < 0 {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			after = parsed
		}
		entries, err := s.database.AgentThreadEntries(r.Context(), userID, spaceID, threadID, after)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
	}
}

func (s *SpacesService) AgentThreadReaction() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Key   string `json:"key"`
			Emoji string `json:"emoji"`
			On    bool   `json:"on"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			return
		}
		if err := s.database.SetAgentThreadReaction(r.Context(), userID, chi.URLParam(r, "spaceID"),
			chi.URLParam(r, "threadID"), body.Key, body.Emoji, body.On); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
