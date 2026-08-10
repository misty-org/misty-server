package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) ActionSuggestionSettings() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			item, err := s.database.SpaceActionSuggestionSettings(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
			return
		}
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.UpdateSpaceActionSuggestionSettings(r.Context(), userID, spaceID, body.Enabled)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func (s *SpacesService) ConversationSuggestionVeto() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			veto, err := s.database.HasSpaceConversationSuggestionVeto(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "conversationID"))
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"veto": veto})
			return
		}
		item, err := s.database.SetSpaceConversationSuggestionVeto(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "conversationID"), r.Method == http.MethodPut)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if item == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func (s *SpacesService) ActionSuggestions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		items, err := s.database.SpaceActionSuggestions(r.Context(), userID, chi.URLParam(r, "spaceID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"suggestions": items})
	}
}

func (s *SpacesService) ActionSuggestionReview() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, batchID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "batchID")
		item, err := s.database.SpaceActionSuggestion(r.Context(), userID, spaceID, batchID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		participating, err := s.database.SpaceSuggestionParticipatingAgentIDs(r.Context(), userID, spaceID, item.Scope)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		memberships, err := s.database.SpaceAgentMemberships(r.Context(), userID, spaceID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		eligible := make([]db.SpaceAgentMembership, 0, len(memberships))
		eligibleByItem := make(map[string][]db.SpaceAgentMembership, len(item.Items))
		for _, membership := range memberships {
			if participating[membership.AgentID] && membership.Enabled {
				eligible = append(eligible, membership)
				for _, suggestionItem := range item.Items {
					if s.database.AuthorizeSuggestionAction(r.Context(), userID, spaceID, membership.AgentID, suggestionItem.RequiredCapability, item.Scope) == nil {
						eligibleByItem[suggestionItem.ID] = append(eligibleByItem[suggestionItem.ID], membership)
					}
				}
			}
		}
		audience := db.SpaceResourceAudience{Kind: db.SpaceAudienceSpace}
		if item.Scope.Kind == db.ConversationScopePrivate {
			audience = db.SpaceResourceAudience{Kind: db.SpaceAudienceConversation, ConversationID: item.Scope.ConversationID}
		}
		writeJSON(w, http.StatusOK, db.SpaceActionSuggestionReview{Suggestion: *item, EligibleAgents: eligible, EligibleAgentsByItem: eligibleByItem, Audience: audience})
	}
}

func (s *SpacesService) DismissActionSuggestion() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.DismissSpaceActionSuggestion(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "batchID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpacesService) ShareResourceWithSpace() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.ShareSpaceResourceWithSpace(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "resourceKind"), chi.URLParam(r, "resourceID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
