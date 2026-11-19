package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// codeSessionTicket is what the app needs to open a live session's code room.
type codeSessionTicket struct {
	Ticket    string    `json:"ticket"`
	Room      string    `json:"room"`
	URL       string    `json:"url"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// codeSessionRole is every member's role in a code room: everyone edits. A closing account
// still joins as a viewer (collaborationRole), so the room takes no edits from it.
const codeSessionRole = "editor"

// SpaceCodeSessions lists a Space's open live code sessions, and starts one. Any member may.
func (s *SpacesService) SpaceCodeSessions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			sessions, err := s.database.OpenSpaceCodeSessions(r.Context(), userID, spaceID)
			if err != nil {
				writeCodeSessionError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
		case http.MethodPost:
			var body struct {
				Name       string `json:"name"`
				RepoKey    string `json:"repoKey"`
				BaseCommit string `json:"baseCommit"`
				Branch     string `json:"branch"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			session, err := s.database.CreateSpaceCodeSession(r.Context(), userID, spaceID, db.NewCodeSession{
				Name: body.Name, RepoKey: body.RepoKey, BaseCommit: body.BaseCommit, Branch: body.Branch,
			})
			if err != nil {
				writeCodeSessionError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, session)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// SpaceCodeSession reads one session, ended ones too. A session of another Space, or one the
// caller is not a member for, answers the same as a missing one.
func (s *SpacesService) SpaceCodeSession() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		session, err := s.database.SpaceCodeSessionByID(
			r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "sessionID"),
		)
		if err != nil {
			writeCodeSessionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, session)
	}
}

// EndSpaceCodeSession ends a session with its outcome. Only the member who started it may.
// Its room then closes every socket as deleted and drops its state, through the outbox.
func (s *SpacesService) EndSpaceCodeSession() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Outcome string `json:"outcome"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		session, err := s.database.EndSpaceCodeSession(
			r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "sessionID"), body.Outcome,
		)
		if err != nil {
			writeCodeSessionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, session)
	}
}

// SpaceCodeSessionCollaborationTicket mints a short-lived credential for the session's code
// room after rechecking membership and that the session is still open.
func (s *SpacesService) SpaceCodeSessionCollaborationTicket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		session, err := s.database.OpenSpaceCodeSessionForRoom(
			r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "sessionID"),
		)
		if err != nil {
			writeCodeSessionError(w, err)
			return
		}
		ticket, err := s.JournalCollab.MintCodeTicket(
			userID, session.SpaceID, session.ID, codeSessionRole, session.ACLVersion,
		)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusCreated, codeSessionTicket{
			Ticket: ticket.Ticket, Room: ticket.Room, URL: ticket.URL,
			Role: ticket.Role, ExpiresAt: ticket.ExpiresAt,
		})
	}
}

func writeCodeSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrCodeSessionBranchInUse):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "branch_in_use"})
	case errors.Is(err, db.ErrCodeSessionEnded):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "session_ended"})
	default:
		writeSpaceError(w, err)
	}
}
