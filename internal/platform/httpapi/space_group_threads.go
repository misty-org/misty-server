package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// ConversationMembers adds people to a group DM that has three or more already.
func (s *SpacesService) ConversationMembers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			UserIDs []string `json:"user_ids"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.AddGroupDirectMembers(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "conversationID"), body.UserIDs)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

// LeaveConversation takes the caller out of a group DM.
func (s *SpacesService) LeaveConversation() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.LeaveGroupDirectConversation(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "conversationID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ConversationAgent lists the caller's agent in a DM (PUT) or takes it out (DELETE).
func (s *SpacesService) ConversationAgent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, conversationID, agentID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "conversationID"), chi.URLParam(r, "agentID")
		if r.Method == http.MethodDelete {
			if err := s.database.RemoveConversationAgent(r.Context(), userID, spaceID, conversationID, agentID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if err := s.database.AddConversationAgent(r.Context(), userID, spaceID, conversationID, agentID, body.Name); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ThreadReplies pages a thread's newest replies, in chronological order within the page.
func (s *SpacesService) ThreadReplies() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if _, hasBefore := r.URL.Query()["before"]; hasBefore {
			before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
			messages, hasOlder, err := s.database.SpaceThreadReplies(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "messageID"), before, limit)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"messages": messages, "has_older": hasOlder})
			return
		}
		// Older clients page forward with `after`; keep that contract while the current client
		// uses a newest-first `before` cursor.
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		messages, err := s.database.SpaceThreadRepliesAfter(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "messageID"), after, limit)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
	}
}

// ThreadRead moves the caller's read position in a thread forward.
func (s *SpacesService) ThreadRead() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Seq int64 `json:"seq"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if err := s.database.MarkSpaceThreadRead(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "messageID"), body.Seq); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// Threads lists the threads the caller follows in a Space.
func (s *SpacesService) Threads() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		threads, err := s.database.SpaceThreads(r.Context(), userID, chi.URLParam(r, "spaceID"), limit)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"threads": threads})
	}
}
