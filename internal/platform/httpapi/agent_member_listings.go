package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// AgentListings lists the agents members published to a Space.
func (s *SpacesService) AgentListings() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		listings, err := s.database.SpaceAgentListings(r.Context(), userID, chi.URLParam(r, "spaceID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"listings": listings})
	}
}

// AgentListing publishes (PUT) or unpublishes (DELETE) one of the caller's
// own agents in a Space they belong to.
func (s *SpacesService) AgentListing() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, agentID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "agentID")
		switch r.Method {
		case http.MethodPut:
			var input db.SpaceAgentListingInput
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&input); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
				return
			}
			listing, err := s.database.SaveSpaceAgentListing(r.Context(), userID, spaceID, agentID, input)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, listing)
		case http.MethodDelete:
			if err := s.database.DeleteSpaceAgentListing(r.Context(), userID, spaceID, agentID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
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

// PendingAgentMemberRequests lists requests waiting for the caller's approval.
func (s *SpacesService) PendingAgentMemberRequests() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		requests, err := s.database.PendingAgentMemberRequests(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"requests": requests})
	}
}

// DecideAgentMemberRequest approves or declines a request for the caller's
// agent. Approving starts the work, which the caller pays for.
func (s *SpacesService) DecideAgentMemberRequest(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		request, err := s.database.DecideAgentMemberRequest(r.Context(), userID, chi.URLParam(r, "requestID"), approve)
		if errors.Is(err, db.ErrAgentListingUnavailable) {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "agent_not_available"})
			return
		}
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"request": request, "state": memberRequestState(request)})
	}
}
