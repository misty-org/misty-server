package api

import (
	"context"
	"net/http"
	"net/url"

	"github.com/kannachi323/misty/server/internal/platform/db"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"strings"
)

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

func (s *SpacesService) SpaceInvitationToken() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(chi.URLParam(r, "token"))
		if token == "" {
			writeSpaceError(w, db.ErrSpaceInviteNotFound)
			return
		}
		tokenHash := security.HashToken(token)
		switch r.Method {
		case http.MethodGet:
			preview, err := s.database.SpaceInvitationPreview(r.Context(), tokenHash)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, preview)
		case http.MethodPost:
			userID, ok := authenticatedUser(w, r, s.database)
			if !ok {
				return
			}
			var body struct {
				Accept bool `json:"accept"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			space, err := s.database.RespondToSpaceInviteToken(
				r.Context(), userID, tokenHash, body.Accept,
			)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			if !body.Accept {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			writeJSON(w, http.StatusOK, space)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}
