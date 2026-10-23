package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

const noteProjectionBodyLimit = 512 * 1024

// JournalNoteProjection accepts a signed callback from the serialized Yjs
// room. It is intentionally separate from member-authenticated note routes.
func (s *SpacesService) JournalNoteProjection() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, noteProjectionBodyLimit+1))
		if err != nil || len(body) > noteProjectionBodyLimit {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		timestamp := r.Header.Get("X-Misty-Timestamp")
		issuedAt, parseErr := strconv.ParseInt(timestamp, 10, 64)
		if parseErr != nil || time.Since(time.Unix(issuedAt, 0)).Abs() > 5*time.Minute ||
			!s.JournalCollab.VerifyProjectionSignature(timestamp, body, r.Header.Get("X-Misty-Signature")) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		var input struct {
			NoteID          string   `json:"note_id"`
			Revision        int64    `json:"revision"`
			Title           string   `json:"title"`
			Markdown        string   `json:"markdown"`
			PlainText       string   `json:"plain_text"`
			OutgoingNoteIDs []string `json:"outgoing_note_ids"`
		}
		if json.Unmarshal(body, &input) != nil {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		applied, err := s.database.ApplySpaceNoteProjection(r.Context(), db.SpaceNoteProjection{
			NoteID: input.NoteID, Revision: input.Revision, Title: input.Title,
			Markdown: input.Markdown, PlainText: input.PlainText, OutgoingNoteIDs: input.OutgoingNoteIDs,
		})
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"applied": applied})
	}
}

// SpaceNotes handles membership-wide native Space notes.
func (s *SpacesService) SpaceNotes() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			notes, err := s.database.AccessibleSpaceNotes(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"notes": notes})
		case http.MethodPost:
			var body struct {
				Title string `json:"title"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			note, err := s.database.CreateSpaceNote(r.Context(), userID, spaceID, body.Title)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, note)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// SpaceNote handles one note: reading it and deleting it. Both behave as
// not-found for any caller without the matching capability, so neither reveals
// that the note exists.
func (s *SpacesService) SpaceNote() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		noteID := chi.URLParam(r, "noteID")
		switch r.Method {
		case http.MethodGet:
			note, err := s.database.SpaceNoteByID(r.Context(), userID, noteID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			if note.SpaceID != chi.URLParam(r, "spaceID") {
				// The note exists but was requested under the wrong Space. Answer
				// as if it does not exist rather than confirming it elsewhere.
				writeSpaceError(w, db.ErrSpaceNotFound)
				return
			}
			writeJSON(w, http.StatusOK, note)
		case http.MethodDelete:
			if err := s.database.DeleteSpaceNote(r.Context(), userID, noteID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPatch:
			var body struct {
				Archived *bool `json:"archived"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			if body.Archived == nil {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			if err := s.database.SetSpaceNoteArchived(
				r.Context(), userID, noteID, *body.Archived,
			); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// SpaceNoteMetadata updates server-owned, non-CRDT metadata. Collaborative
// title and body changes never arrive here: they reach PostgreSQL only as
// projections from the collaboration service.
func (s *SpacesService) SpaceNoteMetadata() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			SharedTags []string `json:"shared_tags"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		noteID := chi.URLParam(r, "noteID")
		if err := s.database.UpdateNoteSharedTags(r.Context(), userID, noteID, body.SharedTags); err != nil {
			writeSpaceError(w, err)
			return
		}
		note, err := s.database.SpaceNoteByID(r.Context(), userID, noteID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, note)
	}
}

func (s *SpacesService) SpaceNoteBacklinks() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		noteID := chi.URLParam(r, "noteID")
		note, err := s.database.SpaceNoteByID(r.Context(), userID, noteID)
		if err != nil || note.SpaceID != chi.URLParam(r, "spaceID") {
			writeSpaceError(w, db.ErrSpaceNotFound)
			return
		}
		links, err := s.database.SpaceNoteBacklinks(r.Context(), userID, noteID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"backlinks": links})
	}
}

// SpaceNoteCollaborationTicket mints a short-lived, single-connection ticket
// for the collaboration service.
//
// Access is rechecked here rather than trusted from the note fetch that
// preceded it, because a grant can be revoked between the two calls.
func (s *SpacesService) SpaceNoteCollaborationTicket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		noteID := chi.URLParam(r, "noteID")
		access, err := s.database.RequireNoteView(r.Context(), userID, noteID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		// The note is re-read here rather than trusted from an earlier request,
		// because acl_version must be the one current at signing time. A ticket
		// carrying a stale version is refused by the room.
		note, err := s.database.SpaceNoteByID(r.Context(), userID, noteID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if note.SpaceID != chi.URLParam(r, "spaceID") {
			writeSpaceError(w, db.ErrSpaceNotFound)
			return
		}
		ticket, err := s.JournalCollab.MintNoteTicket(userID, note.SpaceID, note.ID, access.Role, note.ACLVersion)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		// Tickets are bearer credentials with a 60 second life; no cache may
		// keep one around.
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusCreated, ticket)
	}
}
