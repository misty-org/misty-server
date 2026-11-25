package api

import (
	"context"
	"log"
	"net/http"
	"net/url"

	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/email"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/security"
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
			Email  string `json:"email"`
			UserID string `json:"user_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		token, err := security.GenerateSecureToken()
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		var invite *db.SpaceInvitation
		if body.UserID != "" {
			invite, err = s.database.InviteFriendToSpaceWithToken(
				r.Context(), userID, spaceID, body.UserID, security.HashToken(token),
			)
		} else {
			invite, err = s.database.InviteToSpaceWithToken(
				r.Context(), userID, spaceID, body.Email, security.HashToken(token),
			)
		}
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !invite.ByAccount {
			invite.Link = SpaceInvitationLink(token)
			invite.Emailed = s.emailInvitation(r.Context(), invite)
		}
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
			if err := s.database.CancelInvitationEmail(r.Context(), inviteID); err != nil {
				log.Printf("invitation email not cancelled: %v", err)
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
		if !invite.ByAccount {
			invite.Link = SpaceInvitationLink(token)
			invite.Emailed = s.emailInvitation(r.Context(), invite)
		}
		writeJSON(w, http.StatusOK, invite)
	}
}

func SpaceInvitationLink(token string) string {
	return "misty://open/invite/" + url.PathEscape(token)
}

func (s *SpacesService) emailInvitation(ctx context.Context, invite *db.SpaceInvitation) bool {
	if !email.Configured() {
		return false
	}
	if err := s.database.QueueInvitationEmail(ctx, invite.ID, invite.InvitedEmail, invite.Link, invite.SpaceName, invite.InviterName); err != nil {
		log.Printf("invitation email not stored: %v", err)
		return false
	}
	return true
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

func (s *SpacesService) SpaceInvitationResponse() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		action := chi.URLParam(r, "action")
		if action != "accept" && action != "decline" {
			writeSpaceError(w, db.ErrSpaceInviteNotFound)
			return
		}
		accept := action == "accept"
		space, err := s.database.RespondToSpaceInvite(
			r.Context(), userID, chi.URLParam(r, "inviteID"), accept,
		)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !accept {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, space)
	}
}
