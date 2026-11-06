package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

const voiceNotesMaxOutput = 4000

var voiceNoteModes = map[string]string{"notes": "Notes", "summary": "Summary", "actions": "Action items", "chapters": "Chapters"}

// VoiceTranscriptNotes turns a transcript (plus what the person typed while
// listening) into a Note in their own Space: notes, a summary, action items
// or chapters. The transcript is attached at the end of the Note.
func (s *SpacesService) VoiceTranscriptNotes() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Mode      string `json:"mode"`
			UserNotes string `json:"user_notes"`
			Template  string `json:"template"`
			NoteID    string `json:"note_id"`
			Language  string `json:"language"`
			// A desktop writes the Note on its own model and sends it (docs/agents/runtime.md).
			Result *voiceNotesResult `json:"result"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
		if decodeAIJSON(w, r, &body) != nil || voiceNoteModes[body.Mode] == "" || len(body.UserNotes) > 40_000 || len(body.Template) > 8_000 || len(body.NoteID) > 200 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_voice_notes"})
			return
		}
		transcript, err := s.database.VoiceTranscript(r.Context(), user, chi.URLParam(r, "transcriptID"))
		if err != nil {
			writeVoiceError(w, err)
			return
		}
		if body.Result == nil {
			// First the transcript the desktop writes from, then the Note it wrote.
			writeJSON(w, http.StatusOK, map[string]any{"on_device": true, "input": voiceNotesInput(transcript, body.Mode, body.UserNotes, body.Template, body.Language)})
			return
		}
		written, err := s.writeVoiceNote(r.Context(), user, transcript, body.Mode, body.NoteID, *body.Result)
		if err != nil {
			writeVoiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, written)
	}
}

type voiceNoteWritten struct {
	NoteID      string            `json:"note_id"`
	SpaceID     string            `json:"space_id,omitempty"`
	Title       string            `json:"title"`
	Markdown    string            `json:"markdown"`
	ActionItems []voiceActionItem `json:"action_items"`
	Chapters    []voiceChapter    `json:"chapters"`
}

// writeVoiceNote saves the Note the desktop wrote: over noteID when given,
// otherwise as a new Note in the person's own Space.
func (s *SpacesService) writeVoiceNote(ctx context.Context, user string, transcript db.VoiceTranscript, mode, noteID string, result voiceNotesResult) (voiceNoteWritten, error) {
	var err error
	markdown := voiceNoteMarkdown(transcript, mode, result)
	title := firstNonEmpty(strings.TrimSpace(result.Title), transcript.Title, voiceNoteModes[mode])
	spaceID := ""
	if noteID != "" {
		_, err = s.database.UpdateSpaceNoteContent(ctx, user, noteID, title, markdown)
	} else if spaceID, err = s.personalSpaceID(ctx, user); err == nil {
		var note *db.SpaceNote
		note, err = s.database.CreateSpaceNoteWithAudience(ctx, user, spaceID, title, db.SpaceResourceAudience{Kind: db.SpaceAudienceSpace}, markdown)
		if note != nil {
			noteID = note.ID
		}
	}
	if err != nil {
		return voiceNoteWritten{}, err
	}
	_ = s.database.UpdateVoiceTranscript(ctx, user, transcript.ID, nil, noteID, "")
	return voiceNoteWritten{NoteID: noteID, SpaceID: spaceID, Title: title, Markdown: result.Markdown, ActionItems: result.ActionItems, Chapters: result.Chapters}, nil
}

// voiceNotesInput is what a Note is written from: the transcript with speaker names.
func voiceNotesInput(transcript db.VoiceTranscript, mode, userNotes, template, language string) voiceNotesRequest {
	turns := make([]voiceTurnResult, 0, len(transcript.Turns))
	for _, turn := range transcript.Turns {
		speaker := transcript.Speakers[turn.Speaker]
		if speaker == "" {
			speaker = turn.Speaker
		}
		turns = append(turns, voiceTurnResult{Speaker: speaker, Start: turn.Start, End: turn.End, Text: turn.Text})
	}
	return voiceNotesRequest{
		Mode: mode, Title: transcript.Title, Turns: turns, UserNotes: userNotes, Template: template,
		Language: firstNonEmpty(language, transcript.Language), MaxOutputTokens: voiceNotesMaxOutput,
	}
}

// voiceNoteMarkdown is the Note body: the generated part, then the
// transcript with speaker names, so the source is always one scroll away.
func voiceNoteMarkdown(transcript db.VoiceTranscript, mode string, result voiceNotesResult) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(result.Markdown))
	if mode == "actions" && len(result.ActionItems) > 0 && !strings.Contains(result.Markdown, "- [ ]") {
		b.WriteString("\n\n## Action items\n")
		for _, item := range result.ActionItems {
			line := "- [ ] " + item.Text
			if item.Owner != "" {
				line += " — " + item.Owner
			}
			b.WriteString(line + "\n")
		}
	}
	if mode == "chapters" && len(result.Chapters) > 0 {
		b.WriteString("\n\n## Chapters\n")
		for _, chapter := range result.Chapters {
			fmt.Fprintf(&b, "- **%s** %s — %s\n", clockTime(chapter.Start), chapter.Title, chapter.Summary)
		}
	}
	b.WriteString("\n\n---\n\n## Transcript\n\n")
	for _, turn := range transcript.Turns {
		name := firstNonEmpty(transcript.Speakers[turn.Speaker], turn.Speaker)
		fmt.Fprintf(&b, "**%s** (%s): %s\n\n", name, clockTime(turn.Start), strings.TrimSpace(turn.Text))
	}
	out := strings.TrimSpace(b.String())
	// Notes hold up to 100,000 characters; keep the generated part whole.
	if runes := []rune(out); len(runes) > 99_000 {
		out = string(runes[:99_000]) + "\n\n…"
	}
	return out
}

func clockTime(seconds float64) string {
	total := int(seconds)
	if total >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", total/3600, total/60%60, total%60)
	}
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}
