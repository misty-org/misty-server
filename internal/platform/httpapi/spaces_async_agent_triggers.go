package api

import (
	"context"
	"log"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// The message is already stored by the time triggers are queued, so a failure
// here is reported as a failed run on that message instead of failing the send.
// Turning it into a request error would tell the client nothing was posted
// while the message is visible to everyone else in the Space.
func (s *SpacesService) enqueueSpaceAgentMessageTriggers(
	ctx context.Context,
	requestingUserID, spaceID, conversationID, sourceMessageID, triggerKind string,
	agentIDs []string,
	content []db.MessageSpan,
	fileNodeIDs, attachmentIDs, libraryItemIDs []string,
) []*db.SpaceAgentMessageTrigger {
	triggers := make([]*db.SpaceAgentMessageTrigger, 0, len(agentIDs))
	for _, agentID := range uniqueStrings(agentIDs) {
		trigger, err := s.database.QueueSpaceAgentMessageTrigger(ctx, requestingUserID, spaceID, conversationID, sourceMessageID, agentID, triggerKind)
		if err != nil {
			code, message := spaceRunFailureFromError(err)
			log.Printf("space agent trigger could not be queued for agent %s: %v", agentID, err)
			triggers = append(triggers, &db.SpaceAgentMessageTrigger{
				// Nothing was stored, so this id only has to be stable enough
				// for the client to key and dedupe the inline run notice.
				ID:              "agenttrigger_unqueued_" + sourceMessageID + "_" + agentID,
				AgentID:         agentID,
				State:           "failed",
				ErrorCode:       code,
				ErrorMessage:    message,
				TriggerKind:     triggerKind,
				SourceMessageID: sourceMessageID,
			})
			continue
		}
		triggers = append(triggers, trigger)
		if !trigger.Created {
			continue
		}
		background := context.WithoutCancel(ctx)
		go s.executeSpaceAgentMessageTrigger(background, trigger, requestingUserID, spaceID, conversationID, sourceMessageID, content, fileNodeIDs, attachmentIDs, libraryItemIDs)
	}
	return triggers
}

func (s *SpacesService) executeSpaceAgentMessageTrigger(
	ctx context.Context,
	trigger *db.SpaceAgentMessageTrigger,
	requestingUserID, spaceID, conversationID, sourceMessageID string,
	content []db.MessageSpan,
	fileNodeIDs, attachmentIDs, libraryItemIDs []string,
) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err := s.database.UpdateSpaceAgentMessageTrigger(ctx, trigger.ID, "working", "", "", ""); err != nil {
		return
	}
	_, runID, err := s.runMentionedAgent(ctx, requestingUserID, spaceID, conversationID, trigger.AgentID, sourceMessageID, trigger.TriggerKind, content, fileNodeIDs, attachmentIDs, libraryItemIDs)
	if err != nil {
		code, message := spaceRunFailureFromError(err)
		state := "failed"
		if ctx.Err() == context.Canceled {
			state = "canceled"
		}
		// The trigger row only carries the operator-safe summary, so the cause
		// is lost unless it is logged here.
		log.Printf("space agent run failed (trigger %s, agent %s, run %q, code %s): %v", trigger.ID, trigger.AgentID, runID, code, err)
		_ = s.database.UpdateSpaceAgentMessageTrigger(context.WithoutCancel(ctx), trigger.ID, state, runID, code, message)
		return
	}
	_ = s.database.UpdateSpaceAgentMessageTrigger(context.WithoutCancel(ctx), trigger.ID, "completed", runID, "", "")
}
