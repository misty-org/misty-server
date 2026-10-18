package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
)

const (
	voiceTranscribeTool = "voice.transcribe"
	voiceToNotesTool    = "voice.to_notes"
	// What a tool result carries of the transcript itself.
	voiceToolTranscriptChars = 12_000
)

// voiceToolRegistrations let an agent transcribe a recording already in a
// Library and turn a transcript into a Note. Both work on files that are
// already there: an agent never starts listening to a microphone, a tab or
// the system. Live captures start only from the person's own click.
func (s *SpacesService) voiceToolRegistrations() []agenttools.Registration {
	base := agenttools.Descriptor{
		Version: 1, Risk: serveragent.RiskWrite, Approval: agenttools.ApprovalNone,
		OutputSchema: agentToolObjectOutputSchema(), Layer: agenttools.LayerPublic, Locality: agenttools.LocalityServer, AllowCustomAgent: true,
	}
	id := map[string]any{"type": "string", "minLength": 1, "maxLength": 200}
	transcribe := base
	transcribe.Name = voiceTranscribeTool
	transcribe.AuditEvent = "voice.transcribed"
	transcribe.Description = "Transcribe an audio or video file from a Space Library, with speakers told apart, and save the transcript for the user. " +
		"Returns the transcript id and its text. Use voice_to_notes on the id for notes or a summary. Never records anything new."
	transcribe.InputSchema = agentToolSchema(map[string]any{
		"library_item_id": id, "space": id,
		"language": map[string]any{"type": "string", "maxLength": 16, "description": "Language code if known, such as en or ja. Omit to detect it."},
	}, []string{"library_item_id"})
	notes := base
	notes.Name = voiceToNotesTool
	notes.AuditEvent = "voice.noted"
	notes.Description = "Turn a saved transcript into a Note in the user's own Space: notes, a summary, action items or chapters. " +
		"Pass note_id to rewrite a Note made from the same transcript instead of creating another."
	notes.InputSchema = agentToolSchema(map[string]any{
		"transcript_id": id, "note_id": id,
		"mode":     map[string]any{"type": "string", "enum": []string{"notes", "summary", "actions", "chapters"}},
		"template": map[string]any{"type": "string", "maxLength": 8000, "description": "Headings or layout the user asked for."},
	}, []string{"transcript_id", "mode"})
	return []agenttools.Registration{
		{Descriptor: transcribe, Handler: s.executeVoiceTranscribe},
		{Descriptor: notes, Handler: s.executeVoiceToNotes},
	}
}

func (s *SpacesService) executeVoiceTranscribe(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		LibraryItemID string `json:"library_item_id"`
		Space         string `json:"space"`
		Language      string `json:"language"`
	}
	if json.Unmarshal(request.Arguments, &input) != nil || strings.TrimSpace(input.LibraryItemID) == "" {
		return nil, agenttools.ErrArgumentsInvalid
	}
	if s.library == nil || s.models == nil || !s.models.Enabled() {
		return nil, serveragent.ErrInvalidRequest("transcribing files isn't available right now")
	}
	space, err := s.spaceAdminTarget(ctx, invocation, input.Space)
	if err != nil {
		return nil, err
	}
	transcript, err := s.transcribeLibraryItem(ctx, invocation.UserID, space.ID, strings.TrimSpace(input.LibraryItemID), input.Language)
	if message, ok := err.(voiceFileError); ok {
		return nil, serveragent.ErrInvalidRequest(string(message))
	}
	if err != nil {
		return nil, err
	}
	var text strings.Builder
	for _, turn := range transcript.Turns {
		fmt.Fprintf(&text, "%s (%s): %s\n", turn.Speaker, clockTime(turn.Start), strings.TrimSpace(turn.Text))
		if text.Len() > voiceToolTranscriptChars {
			text.WriteString("…\n")
			break
		}
	}
	return json.Marshal(map[string]any{
		"transcript_id": transcript.ID, "title": transcript.Title, "language": transcript.Language,
		"duration": clockTime(float64(transcript.DurationMS) / 1000), "transcript": text.String(),
	})
}

func (s *SpacesService) executeVoiceToNotes(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		TranscriptID string `json:"transcript_id"`
		NoteID       string `json:"note_id"`
		Mode         string `json:"mode"`
		Template     string `json:"template"`
	}
	if json.Unmarshal(request.Arguments, &input) != nil || voiceNoteModes[input.Mode] == "" {
		return nil, agenttools.ErrArgumentsInvalid
	}
	if s.models == nil || !s.models.Enabled() {
		return nil, serveragent.ErrInvalidRequest("notes from speech aren't available right now")
	}
	transcript, err := s.database.VoiceTranscript(ctx, invocation.UserID, strings.TrimSpace(input.TranscriptID))
	if err != nil {
		return nil, serveragent.ErrInvalidRequest("no transcript " + input.TranscriptID + " that you can read")
	}
	written, err := s.writeVoiceNote(ctx, invocation.UserID, transcript, input.Mode, "", input.Template, strings.TrimSpace(input.NoteID), "")
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"note_id": written.NoteID, "title": written.Title, "action_items": written.ActionItems})
}
