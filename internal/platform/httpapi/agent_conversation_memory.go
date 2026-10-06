package api

import (
	"context"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const agentConversationMemoryCharacterLimit = 16_000

type agentConversationContext struct {
	Transcript string
}

func (s *SpacesService) agentConversationContext(ctx context.Context, run *db.SpaceRun) (agentConversationContext, error) {
	messages, err := s.database.SpaceConversationMessages(ctx, run.OwnerUserID, run.SpaceID, run.SourceConversationID, 0, 30)
	if err != nil {
		return agentConversationContext{}, err
	}
	conversation := agentConversationContext{}
	// Messages arrive newest-first; render them oldest-first.
	lines := make([]string, 0, len(messages))
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.ID == run.SourceMessageID {
			continue
		}
		text := strings.TrimSpace(renderMessageText(message.Content))
		if text == "" {
			continue
		}
		role := "Person"
		if message.SenderKind == "agent" {
			role = "Agent"
		}
		name := strings.TrimSpace(message.SenderName)
		if name == "" {
			name = role
		}
		lines = append(lines, role+" "+name+": "+text)
	}
	runes := []rune(strings.Join(lines, "\n"))
	if len(runes) > agentConversationMemoryCharacterLimit {
		runes = runes[len(runes)-agentConversationMemoryCharacterLimit:]
	}
	conversation.Transcript = strings.TrimSpace(string(runes))
	return conversation, nil
}
