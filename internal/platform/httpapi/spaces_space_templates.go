package api

import (
	"context"
	"net/http"
	"net/url"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func (s *SpacesService) SpaceTemplates() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		providers := TestingProviderOAuthAvailabilityCatalog()
		writeJSON(w, http.StatusOK, map[string]any{
			"templates": db.BuiltInSpaceTemplates(),
			"providers": providers,
		})
	}
}

func (s *SpacesService) SpaceSetup() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			setup, err := s.database.SpaceSetup(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, setup)
		case http.MethodPatch:
			var body struct {
				Provider string `json:"provider"`
				Status   string `json:"status"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			if err := s.database.SetSpaceSetupProviderStatus(r.Context(), userID, spaceID, body.Provider, body.Status); err != nil {
				writeSpaceError(w, err)
				return
			}
			setup, err := s.database.SpaceSetup(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, setup)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) Space() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			space, err := s.database.SpaceByID(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, space)
		case http.MethodPatch, http.MethodPut:
			var body struct {
				Name string `json:"name"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			space, err := s.database.RenameSpace(r.Context(), userID, spaceID, body.Name)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, space)
		case http.MethodDelete:
			var body struct {
				Confirmation string `json:"confirmation"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			if err := s.database.DeleteSpace(r.Context(), userID, spaceID, body.Confirmation); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) Members() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		members, err := s.database.SpaceMembers(r.Context(), userID, spaceID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		agents, err := s.database.SpaceAgentMemberships(r.Context(), userID, spaceID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"members": members, "agents": agents})
	}
}

func (s *SpacesService) Invite() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			invitations, err := s.database.PendingSpaceInvitations(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"invitations": invitations})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Email string `json:"email"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		token, err := security.GenerateSecureToken()
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		invite, err := s.database.InviteToSpaceWithToken(
			r.Context(), userID, spaceID, body.Email, security.HashToken(token),
		)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		status := s.deliverSpaceInvitation(r.Context(), invite, token)
		_ = s.database.SetSpaceInvitationDelivery(r.Context(), invite.ID, status)
		invite.DeliveryStatus = status
		writeJSON(w, http.StatusCreated, invite)
	}
}

func (s *SpacesService) SpaceInvitationItem() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, inviteID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "inviteID")
		if r.Method == http.MethodDelete {
			if err := s.database.RevokeSpaceInvitation(r.Context(), userID, spaceID, inviteID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		token, err := security.GenerateSecureToken()
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		invite, err := s.database.RefreshSpaceInvitation(
			r.Context(), userID, spaceID, inviteID, security.HashToken(token),
		)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		status := s.deliverSpaceInvitation(r.Context(), invite, token)
		_ = s.database.SetSpaceInvitationDelivery(r.Context(), invite.ID, status)
		invite.DeliveryStatus = status
		writeJSON(w, http.StatusOK, invite)
	}
}

func (s *SpacesService) deliverSpaceInvitation(
	ctx context.Context,
	invite *db.SpaceInvitation,
	token string,
) string {
	if s.invitationSender == nil || s.invitationBaseURL == "" {
		return "failed"
	}
	link := s.invitationBaseURL + "/" + url.PathEscape(token)
	if err := s.invitationSender.SendSpaceInvitationEmail(
		ctx, invite.InvitedEmail, invite.InviterName, invite.SpaceName, link,
	); err != nil {
		return "failed"
	}
	return "sent"
}
