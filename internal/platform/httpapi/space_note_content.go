package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

const (
	// A note's body is up to 100,000 characters, which JSON can spell in far more
	// bytes than the 8 KiB other requests get.
	noteContentBodyLimit = 512 << 10
	noteMarkdownMaxChars = 100_000
)

// SpaceNoteContent replaces a note's body or adds to its end. It takes the
// durable path agents and imports use: the note stays editable by people in the
// room, and the replacement reaches them through the collaboration outbox.
func (s *SpacesService) SpaceNoteContent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Title    string `json:"title"`
			Markdown string `json:"markdown"`
			Append   string `json:"append"`
		}
		if decodeNoteContent(w, r, &body) != nil {
			return
		}
		if (body.Markdown != "" && body.Append != "") || len([]rune(body.Markdown)) > noteMarkdownMaxChars || len([]rune(body.Append)) > noteMarkdownMaxChars {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		noteID := chi.URLParam(r, "noteID")
		current, err := s.database.SpaceNoteByID(r.Context(), userID, noteID)
		if err == nil && current.SpaceID != chi.URLParam(r, "spaceID") {
			err = db.ErrSpaceNotFound
		}
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		title, markdown := strings.TrimSpace(body.Title), body.Markdown
		if title == "" {
			title = current.TitleProjection
		}
		switch {
		case body.Append != "" && strings.TrimSpace(current.MarkdownProjection) != "":
			markdown = strings.TrimRight(current.MarkdownProjection, "\n") + "\n\n" + body.Append
		case body.Append != "":
			markdown = body.Append
		case markdown == "":
			markdown = current.MarkdownProjection
		}
		note, err := s.database.UpdateSpaceNoteContent(r.Context(), userID, noteID, title, markdown)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, noteWriteResult(note, title, markdown, "update"))
	}
}

func decodeNoteContent(w http.ResponseWriter, r *http.Request, dst any) error {
	if err := decodeStrictJSONWithLimit(w, r, dst, noteContentBodyLimit); err != nil {
		return rejectInvalidJSON(w)
	}
	return nil
}

// createSpaceNote makes a note, starting from a Markdown body when there is one.
func (s *SpacesService) createSpaceNote(w http.ResponseWriter, r *http.Request, userID, spaceID string) {
	var body struct {
		Title    string `json:"title"`
		Markdown string `json:"markdown"`
	}
	if decodeNoteContent(w, r, &body) != nil {
		return
	}
	if len([]rune(body.Markdown)) > noteMarkdownMaxChars {
		writeSpaceError(w, db.ErrSpaceInvalid)
		return
	}
	if strings.TrimSpace(body.Markdown) == "" {
		note, err := s.database.CreateSpaceNote(r.Context(), userID, spaceID, body.Title)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, note)
		return
	}
	note, err := s.database.CreateSpaceNoteWithAudience(r.Context(), userID, spaceID, body.Title, db.SpaceResourceAudience{Kind: db.SpaceAudienceSpace}, body.Markdown)
	if err != nil {
		writeSpaceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, noteWriteResult(note, body.Title, body.Markdown, "create"))
}

// drawingSceneBodyLimit matches what one drawing change could always carry.
const drawingSceneBodyLimit = 256 << 10

// SpaceDrawingScene reads a drawing's live scene from its collaboration room, or
// applies a change to it. People draw over the room's socket; this is the same
// room's control command for a client that wants the scene as data.
func (s *SpacesService) SpaceDrawingScene() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		drawing, err := s.database.SpaceDrawingByID(r.Context(), userID, chi.URLParam(r, "drawingID"))
		if err == nil && drawing.SpaceID != chi.URLParam(r, "spaceID") {
			err = db.ErrSpaceNotFound
		}
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		command, payload := "drawing_scene_read", mustRawJSON(map[string]any{"include_deleted": r.URL.Query().Get("include_deleted") == "true"})
		if r.Method == http.MethodPost {
			access, accessErr := s.database.DrawingAccessFor(r.Context(), userID, drawing.ID)
			if accessErr == nil && !access.CanEdit {
				accessErr = db.ErrSpaceForbidden
			}
			if accessErr != nil {
				writeSpaceError(w, accessErr)
				return
			}
			var change map[string]json.RawMessage
			if err := decodeStrictJSONWithLimit(w, r, &change, drawingSceneBodyLimit); err != nil || !validDrawingSceneChange(change) {
				rejectInvalidJSON(w)
				return
			}
			if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
				change["request_id"] = mustRawJSON(drawingSceneRequestID(userID, key))
			}
			command, payload = "drawing_scene_apply", mustRawJSON(change)
		}
		result, err := s.requestCollaborationControlCommand(r.Context(), "drawing-room", s.JournalCollab.DrawingRoomID(drawing.ID), drawing.ID, command, payload)
		if err != nil {
			writeDrawingSceneError(w, err)
			return
		}
		var response map[string]any
		if err := json.Unmarshal(result, &response); err != nil {
			writeSpaceError(w, err)
			return
		}
		response["drawing"] = drawing
		writeJSON(w, http.StatusOK, response)
	}
}

// validDrawingSceneChange holds to the fields a scene change can have, so the
// room's own fields, such as request_id, can't be set from outside.
func validDrawingSceneChange(change map[string]json.RawMessage) bool {
	for field := range change {
		switch field {
		case "base_hash", "mode", "elements", "delete_element_ids", "scene":
		default:
			return false
		}
	}
	return true
}

func drawingSceneRequestID(userID, key string) string {
	digest := sha256.Sum256([]byte(userID + "\n" + key))
	return "client:" + hex.EncodeToString(digest[:])
}

// writeDrawingSceneError names the room's answers a client can act on.
func writeDrawingSceneError(w http.ResponseWriter, err error) {
	text := err.Error()
	switch {
	case errors.Is(err, ErrCollaborationUnconfigured):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "collaboration_unconfigured"})
	case strings.Contains(text, "drawing_conflict"):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "drawing_conflict"})
	case strings.Contains(text, "document_too_large"):
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"code": "document_too_large"})
	case strings.HasPrefix(text, "collaboration service returned 400"):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
	default:
		writeSpaceError(w, err)
	}
}

func noteWriteResult(note *db.SpaceNote, title, markdown, operation string) json.RawMessage {
	digest := sha256.Sum256([]byte(markdown))
	return mustRawJSON(struct {
		*db.SpaceNote
		ContentReceipt map[string]any `json:"content_receipt"`
	}{
		SpaceNote: note,
		ContentReceipt: map[string]any{
			"operation":           operation,
			"title":               title,
			"markdown_characters": len([]rune(markdown)),
			"markdown_sha256":     hex.EncodeToString(digest[:]),
			"state":               "queued_for_collaboration",
		},
	})
}
