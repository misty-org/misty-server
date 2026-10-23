package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/modelruntime"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

func TestVoiceToolsWorkOnFilesAndPassTheAskGate(t *testing.T) {
	service := &SpacesService{}
	registrations := service.voiceToolRegistrations()
	if len(registrations) != 2 || registrations[0].Descriptor.Name != voiceTranscribeTool || registrations[1].Descriptor.Name != voiceToNotesTool {
		t.Fatalf("voice tools = %+v", registrations)
	}
	for _, registration := range registrations {
		descriptor := registration.Descriptor
		// Both spend and save, so Ask pauses them; neither can start a capture.
		if descriptor.Risk != agent.RiskWrite || !gatedByAsk(descriptor) || strings.Contains(string(descriptor.InputSchema), "mic") {
			t.Fatalf("%s is not a gated file-only tool", descriptor.Name)
		}
	}
	ctx := context.Background()
	if _, err := service.executeVoiceToNotes(ctx, agenttools.Invocation{UserID: "u"}, agent.ToolRequest{Arguments: []byte(`{"transcript_id":"t","mode":"poem"}`)}); !errors.Is(err, agenttools.ErrArgumentsInvalid) {
		t.Fatalf("unknown mode = %v", err)
	}
	if _, err := service.executeVoiceTranscribe(ctx, agenttools.Invocation{UserID: "u"}, agent.ToolRequest{Arguments: []byte(`{}`)}); !errors.Is(err, agenttools.ErrArgumentsInvalid) {
		t.Fatalf("missing item = %v", err)
	}
}

func TestVoiceNoteKeepsTheTranscriptWithRenamedSpeakers(t *testing.T) {
	transcript := db.VoiceTranscript{
		Speakers: map[string]string{"Speaker 1": "Ana"},
		Turns:    []db.VoiceTurn{{Speaker: "Speaker 1", Start: 65, Text: "Ship Friday."}, {Speaker: "Speaker 2", Start: 3700, Text: "Agreed."}},
	}
	markdown := voiceNoteMarkdown(transcript, "actions", modelruntime.VoiceNotesResult{Markdown: "## Decisions\n- Ship Friday", ActionItems: []modelruntime.ActionItem{{Text: "Tag the release", Owner: "Ana"}}})
	for _, want := range []string{"## Decisions", "- [ ] Tag the release — Ana", "## Transcript", "**Ana** (1:05): Ship Friday.", "**Speaker 2** (1:01:40): Agreed."} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("note is missing %q:\n%s", want, markdown)
		}
	}
}
