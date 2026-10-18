package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/modelruntime"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
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
		}
		r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
		if decodeAIJSON(w, r, &body) != nil || voiceNoteModes[body.Mode] == "" || len(body.UserNotes) > 40_000 || len(body.Template) > 8_000 || len(body.NoteID) > 200 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_voice_notes"})
			return
		}
		if s.models == nil || !s.models.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "voice_notes_unavailable", "message": "Notes from speech aren't available right now."})
			return
		}
		transcript, err := s.database.VoiceTranscript(r.Context(), user, chi.URLParam(r, "transcriptID"))
		if err != nil {
			writeVoiceError(w, err)
			return
		}
		written, err := s.writeVoiceNote(r.Context(), user, transcript, body.Mode, body.UserNotes, body.Template, body.NoteID, body.Language)
		if err != nil {
			writeVoiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, written)
	}
}

type voiceNoteWritten struct {
	NoteID      string                    `json:"note_id"`
	SpaceID     string                    `json:"space_id,omitempty"`
	Title       string                    `json:"title"`
	Markdown    string                    `json:"markdown"`
	ActionItems []modelruntime.ActionItem `json:"action_items"`
	Chapters    []modelruntime.Chapter    `json:"chapters"`
}

// writeVoiceNote generates the Note and saves it: over noteID when given,
// otherwise as a new Note in the person's own Space.
func (s *SpacesService) writeVoiceNote(ctx context.Context, user string, transcript db.VoiceTranscript, mode, userNotes, template, noteID, language string) (voiceNoteWritten, error) {
	result, err := s.voiceNotes(ctx, user, transcript, mode, userNotes, template, language)
	if err != nil {
		return voiceNoteWritten{}, err
	}
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

func (s *SpacesService) voiceNotes(ctx context.Context, user string, transcript db.VoiceTranscript, mode, userNotes, template, language string) (modelruntime.VoiceNotesResult, error) {
	turns := make([]modelruntime.Turn, 0, len(transcript.Turns))
	size := len(userNotes) + len(template)
	for _, turn := range transcript.Turns {
		speaker := transcript.Speakers[turn.Speaker]
		if speaker == "" {
			speaker = turn.Speaker
		}
		turns = append(turns, modelruntime.Turn{Speaker: speaker, Start: turn.Start, End: turn.End, Text: turn.Text})
		size += len(turn.Text) + len(speaker) + 16
	}
	model := defaultRoleModel("voice-notes")
	provider := aimodels.UsageProvider(model)
	key := "voice-notes:" + uuid.NewString()
	var reservation *serveragent.UsageReservation
	if s.usageMeter != nil {
		var err error
		if reservation, err = serveragent.ReserveMeasuredUsage(s.usageMeter, user, key, provider, model, map[string]int64{"input_bytes": int64(size), "output_tokens": voiceNotesMaxOutput}, ""); err != nil {
			return modelruntime.VoiceNotesResult{}, err
		}
	}
	result, err := s.models.VoiceNotes(ctx, modelruntime.VoiceNotesRequest{
		Route: modelruntime.Instance(), Model: model, Mode: mode, Title: transcript.Title, Turns: turns,
		UserNotes: userNotes, Template: template, Language: firstNonEmpty(language, transcript.Language), MaxOutputTokens: voiceNotesMaxOutput,
	})
	if settleErr := serveragent.FinishMeteredCall(s.usageMeter, reservation, key+":settle", "voice_notes", provider, model, result.Usage, err); settleErr != nil && err == nil {
		return result, settleErr
	}
	return result, err
}

// voiceNoteMarkdown is the Note body: the generated part, then the
// transcript with speaker names, so the source is always one scroll away.
func voiceNoteMarkdown(transcript db.VoiceTranscript, mode string, result modelruntime.VoiceNotesResult) string {
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
