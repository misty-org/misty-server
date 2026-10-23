package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// SpaceAgents lists the agents in a Space, each under the person who owns it.
func (s *SpacesService) SpaceAgents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		agents, err := s.database.SpaceAgents(r.Context(), userID, chi.URLParam(r, "spaceID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
	}
}

// MySpaceAgents lists the agents in every Space the caller belongs to.
func (s *SpacesService) MySpaceAgents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		agents, err := s.database.SpaceAgents(r.Context(), userID, "")
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
	}
}

// AgentMemberRequest shows one request to the member who sent it or the owner
// of the agent doing it.
func (s *SpacesService) AgentMemberRequest() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		request, err := s.database.AgentMemberRequestForUser(r.Context(), userID, chi.URLParam(r, "requestID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"request": request, "state": memberRequestState(request), "view": agentRequestView(request)})
	}
}

// AgentMentions lists the caller's own @mentions of agents with how each run
// went, for the Mentions list on the Agents page.
func (s *SpacesService) AgentMentions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		requests, err := s.database.AgentMentionsForUser(r.Context(), userID, 100)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		mentions := make([]map[string]any, 0, len(requests))
		for index := range requests {
			request := &requests[index]
			mentions = append(mentions, map[string]any{"request": request, "state": memberRequestState(request), "view": agentRequestView(request)})
		}
		writeJSON(w, http.StatusOK, map[string]any{"mentions": mentions})
	}
}

// AgentRequestTranscript shows the steps behind an agent's reply to the person
// who mentioned it, so they can see how it was done. No one else may.
func (s *SpacesService) AgentRequestTranscript() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		steps, err := s.database.AgentRequestTranscript(r.Context(), userID, chi.URLParam(r, "requestID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"steps": steps})
	}
}
