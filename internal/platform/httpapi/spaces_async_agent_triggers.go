package api

import (
	"context"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type explicitAgentInvocation struct {
	AgentID           string                            `json:"agent_id"`
	Mode              string                            `json:"mode,omitempty"`
	Timezone          string                            `json:"timezone,omitempty"`
	ContextReferences []db.CreatorAgentContextReference `json:"context_references,omitempty"`
	ContextNoteID     string                            `json:"context_note_id,omitempty"`
}

func (s *SpacesService) queueExplicitAgentInvocations(ctx context.Context, userID, spaceID, conversationID, sourceMessageID, sourceType, inputModality string, invocations []explicitAgentInvocation, content []db.MessageSpan) []any {
	results := []any{}
	instruction := renderMessageText(content)
	for _, invocation := range invocations {
		run, err := s.database.CreateCreatorAgentRun(ctx, userID, spaceID, invocation.AgentID, db.CreatorAgentRunInput{Instruction: instruction, Mode: invocation.Mode, ConversationTarget: conversationID, ContextReferences: invocation.ContextReferences, ContextNoteID: invocation.ContextNoteID, SourceMessageID: sourceMessageID, SourceType: sourceType, InputModality: inputModality, Timezone: invocation.Timezone})
		if err != nil {
			code, message := spaceRunFailureFromError(err)
			results = append(results, map[string]any{"agent_id": invocation.AgentID, "state": "failed", "error_code": code, "error_message": message})
			continue
		}
		results = append(results, run)
	}
	return results
}
