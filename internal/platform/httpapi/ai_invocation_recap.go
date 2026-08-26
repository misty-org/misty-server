package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func publicAgentRuntimeFailure(code, _ string) string {
	switch strings.TrimSpace(code) {
	case "agent_runtime_timeout":
		return "Misty timed out before completing this request."
	case "authorization_or_state_changed":
		return "Misty stopped because access or task state changed. Please try again."
	default:
		return "Misty could not complete this request."
	}
}

func (s *SpacesService) completeAIInvocationRecap(ctx context.Context, record *db.AIInvocationRecord, prepared *preparedAIInvocationRuntime, answer string, runErr error) error {
	if record == nil {
		return nil
	}
	var body aiInvocationInput
	if json.Unmarshal(record.RequestPayload, &body) != nil || body.Trigger != "schedule" {
		return nil
	}
	items, err := s.database.AIRecaps(ctx, record.UserID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.SurfaceID != body.SurfaceID {
			continue
		}
		var citationsJSON json.RawMessage
		if prepared != nil && strings.TrimSpace(answer) != "" {
			citations, _ := json.Marshal(mistyAnswerCitations(answer, prepared.resolved))
			citationsJSON = citations
		}
		return s.database.CompleteAIRecap(ctx, item, record.ID, answer, citationsJSON, runErr, time.Now().UTC())
	}
	return nil
}
