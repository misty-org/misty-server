package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// The run notebook (docs/agents/harness.md, Sub-agents and the notebook): a plan and
// findings with sources that live outside the model's context, so long runs
// re-read them after compaction and sub-agents report into them. Each write
// shows in the conversation and is mirrored to a note in the personal Space.

const (
	toolboxNotebookRead  = "log.work.read"
	toolboxNotebookWrite = "log.work.write"
)

type aiNotebookEvent struct {
	Plan     string `json:"plan"`
	Findings string `json:"findings"`
	NoteID   string `json:"noteId,omitempty"`
}

func notebookToolDescriptors() []agenttools.Descriptor {
	return []agenttools.Descriptor{
		{Name: toolboxNotebookRead, Version: 1, Risk: agent.RiskRead, Idempotent: true, Layer: agenttools.LayerPublic, Locality: agenttools.LocalityServer,
			Description: "Read this task's notebook: its plan and its findings so far, including what sub-agents reported. Read it before continuing a long task, after earlier steps were summarized, and before writing the final answer.",
			InputSchema: MustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}), OutputSchema: agentToolObjectOutputSchema()},
		{Name: toolboxNotebookWrite, Version: 1, Risk: agent.RiskWrite, Idempotent: true, Layer: agenttools.LayerPublic, Locality: agenttools.LocalityServer, AuditEvent: "agent.notebook.written",
			Description: "Write to this task's notebook, which the person sees in the conversation. section plan holds the steps and their status as a short Markdown checklist; findings holds facts as you learn them, each with its source URL. mode replace rewrites the section; append adds to the end. Keep the plan current on multi-step tasks and add findings as you go, so nothing is lost if earlier steps are summarized.",
			InputSchema: MustAPIRawJSON(map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"section", "content"},
				"properties": map[string]any{
					"section": map[string]any{"type": "string", "enum": []string{"plan", "findings"}},
					"content": map[string]any{"type": "string", "minLength": 1, "maxLength": 20000},
					"mode":    map[string]any{"type": "string", "enum": []string{"replace", "append"}, "description": "Default append for findings, replace for plan."},
				},
			}), OutputSchema: agentToolObjectOutputSchema()},
	}
}

// notebookKey is the notebook a run writes to: its parent's for a sub-agent,
// else its conversation's (or its own when it has none).
func notebookKey(record *db.AIInvocationRecord, body aiInvocationInput) string {
	if key := strings.TrimSpace(body.NotebookKey); key != "" {
		return key
	}
	if record.ConversationID != "" {
		return "conversation:" + record.ConversationID
	}
	return "invocation:" + record.ID
}

func (s *SpacesService) notebookToolRegistrations(record *db.AIInvocationRecord, body aiInvocationInput) []agenttools.Registration {
	if record == nil {
		return nil
	}
	key := notebookKey(record, body)
	descriptors := notebookToolDescriptors()
	return []agenttools.Registration{
		{Descriptor: descriptors[0], Handler: func(ctx context.Context, invocation agenttools.Invocation, _ agent.ToolRequest) (json.RawMessage, error) {
			notebook, err := s.database.AgentNotebook(ctx, invocation.UserID, key)
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"plan": notebook.Plan, "findings": notebook.Findings})
		}},
		{Descriptor: descriptors[1], Handler: func(ctx context.Context, invocation agenttools.Invocation, request agent.ToolRequest) (json.RawMessage, error) {
			var input struct {
				Section string `json:"section"`
				Content string `json:"content"`
				Mode    string `json:"mode"`
			}
			if json.Unmarshal(request.Arguments, &input) != nil || strings.TrimSpace(input.Content) == "" {
				return nil, agenttools.ErrArgumentsInvalid
			}
			appendText := input.Mode == "append" || (input.Mode == "" && input.Section == "findings")
			notebook, err := s.database.WriteAgentNotebook(ctx, invocation.UserID, key, input.Section, input.Content, appendText)
			if errors.Is(err, db.ErrNotebookFull) {
				return nil, agent.ErrInvalidRequest(err.Error())
			}
			if err != nil {
				return nil, err
			}
			s.publishNotebook(ctx, record, notebook)
			return json.Marshal(map[string]any{"saved": true, "section": input.Section, "chars": len(map[string]string{"plan": notebook.Plan, "findings": notebook.Findings}[input.Section])})
		}},
	}
}

// notebookMarkdown is the note people keep: the findings. The plan is the
// agent's own working checklist; people see a plan only in Plan mode.
func notebookMarkdown(notebook *db.AgentNotebook) string {
	return strings.TrimSpace(notebook.Findings) + "\n"
}

// publishNotebook shows the notebook's findings in the conversation that owns
// it and mirrors them to a note. Neither step changes the write's own result.
func (s *SpacesService) publishNotebook(ctx context.Context, record *db.AIInvocationRecord, notebook *db.AgentNotebook) {
	if strings.TrimSpace(notebook.Findings) == "" {
		return
	}
	// A sub-agent's notebook belongs to its parent's conversation.
	target := record
	if parent, err := s.database.DelegationParent(ctx, record.UserID, record.ID); err == nil && parent != "" {
		if parentRecord, lookupErr := s.database.AIInvocationByID(ctx, record.UserID, parent); lookupErr == nil {
			target = parentRecord
		}
	}
	noteID := s.mirrorNotebookNote(ctx, record.UserID, target, notebook)
	if s.aiInvocations == nil || aiInvocationTerminal(target.State) {
		return
	}
	if _, err := s.aiInvocations.restoreDurable(ctx, *target); err != nil {
		return
	}
	_ = s.aiInvocations.append(target.ID, aiInvocationEvent{Type: "notebook.updated", Notebook: &aiNotebookEvent{Findings: notebook.Findings, NoteID: noteID}})
}

func (s *SpacesService) mirrorNotebookNote(ctx context.Context, user string, owner *db.AIInvocationRecord, notebook *db.AgentNotebook) string {
	title := "Notebook"
	if owner.ConversationID != "" {
		if name := strings.TrimSpace(s.database.ConversationTitle(ctx, user, owner.ConversationID)); name != "" {
			title = "Notebook · " + truncateAgentRuntimeText(name, 120)
		}
	}
	markdown := notebookMarkdown(notebook)
	if notebook.NoteID != "" {
		if _, err := s.database.UpdateSpaceNoteContent(ctx, user, notebook.NoteID, title, markdown); err == nil {
			return notebook.NoteID
		}
	}
	spaces, err := s.database.ListSpaces(ctx, user)
	if err != nil {
		return ""
	}
	for _, space := range spaces {
		if !space.IsDefault || space.OwnerUserID != user {
			continue
		}
		note, err := s.database.CreateSpaceNoteWithAudience(ctx, user, space.ID, title, db.SpaceResourceAudience{Kind: db.SpaceAudienceSpace}, markdown)
		if err != nil {
			log.Printf("notebook note for %s: %v", notebook.Key, err)
			return ""
		}
		_ = s.database.SetAgentNotebookNote(ctx, user, notebook.Key, note.ID)
		return note.ID
	}
	return ""
}
