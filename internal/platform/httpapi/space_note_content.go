package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

const (
	noteContentBodyLimit = 512 << 10
	noteMarkdownMaxChars = 100_000
)

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

func (s *SpacesService) createSpaceNote(w http.ResponseWriter, r *http.Request, userID, spaceID string) {
	var body struct {
		Title      string `json:"title"`
		Markdown   string `json:"markdown"`
		Visibility string `json:"visibility"`
	}
	if decodeNoteContent(w, r, &body) != nil {
		return
	}
	if len([]rune(body.Markdown)) > noteMarkdownMaxChars {
		writeSpaceError(w, db.ErrSpaceInvalid)
		return
	}
	space := db.SpaceResourceAudience{Kind: db.SpaceAudienceSpace}
	if strings.TrimSpace(body.Markdown) == "" {
		note, err := s.database.CreateSpaceNoteWithVisibility(r.Context(), userID, spaceID, body.Title, space, "", body.Visibility)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, note)
		return
	}
	note, err := s.database.CreateSpaceNoteWithVisibility(r.Context(), userID, spaceID, body.Title, space, body.Markdown, body.Visibility)
	if err != nil {
		writeSpaceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, noteWriteResult(note, body.Title, body.Markdown, "create"))
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
