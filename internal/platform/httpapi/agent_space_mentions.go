package api

import (
	"context"
	"errors"
	"log"
	"slices"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// startAgentMentionRequests turns @mentions of agent members in a Space chat
// message into requests to those agents. Each runs under its owner's rules for
// the Space (off, ask first, or start right away) and is billed to the sender.
// The agent's reply, or why there is none, posts under the message. A request
// that can't start never fails the message itself.
func (s *SpacesService) startAgentMentionRequests(ctx context.Context, userID string, message *db.SpaceMessage, agentIDs []string) {
	if message == nil || len(agentIDs) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	text := renderMessageText(message.Content)
	seen := []string{}
	for _, agentID := range agentIDs {
		if slices.Contains(seen, agentID) || len(seen) >= 3 {
			continue
		}
		seen = append(seen, agentID)
		request, err := s.database.CreateAgentMemberRequest(ctx, db.AgentMemberRequestInput{
			SpaceID: message.SpaceID, RequesterUserID: userID, RequesterRunID: db.MentionRequester(message.ID),
			TargetAgentID: agentID, Message: text, IdempotencyKey: agentID, SourceConversationID: message.ConversationID,
		})
		if err != nil {
			s.explainUnstartedMention(ctx, userID, message, agentID, err)
			continue
		}
		if request.Replay || request.Approval != "pending" {
			continue
		}
		owner := firstName(request.TargetOwnerName, "its owner")
		if _, err := s.database.CreateAgentMemberRequestMessage(ctx, request, "agent_member_request_pending", "Waiting for "+owner+" to approve this request."); err != nil {
			log.Printf("agent mention: pending note for %s failed: %v", request.ID, err)
		}
	}
}

// explainUnstartedMention says in the chat why a mentioned agent won't answer,
// when it is a member that turned the request down. Mentions of agents that
// aren't members of this Space are ordinary text.
func (s *SpacesService) explainUnstartedMention(ctx context.Context, userID string, message *db.SpaceMessage, agentID string, cause error) {
	reason := ""
	switch {
	case errors.Is(cause, db.ErrAgentListingUnavailable):
		reason = "I'm not taking requests here right now."
	case errors.Is(cause, db.ErrAgentRequestBusy):
		reason = "I have too many requests open. Try again when some finish."
	case errors.Is(cause, db.ErrAgentRequestLimit):
		reason = "You've sent a lot of requests in the last hour. Try again later."
	default:
		log.Printf("agent mention: request to %s failed: %v", agentID, cause)
		return
	}
	listings, err := s.database.SpaceAgentListings(ctx, userID, message.SpaceID)
	if err != nil {
		return
	}
	for _, listing := range listings {
		if listing.AgentID != agentID {
			continue
		}
		request := &db.AgentMemberRequest{SpaceID: message.SpaceID, TargetAgentID: agentID, TargetOwnerUserID: listing.OwnerUserID,
			SourceMessageID: message.ID, SourceConversationID: message.ConversationID}
		if _, err := s.database.CreateAgentMemberRequestMessage(ctx, request, "agent_member_request_unavailable", reason); err != nil {
			log.Printf("agent mention: unavailable note for %s failed: %v", agentID, err)
		}
		return
	}
}

// mentionRequestForRun finds the chat mention a run is answering, if any.
func (s *SpacesService) mentionRequestForRun(ctx context.Context, run *db.SpaceRun) *db.AgentMemberRequest {
	if run == nil || run.TriggerKind != "delegated" {
		return nil
	}
	request, err := s.database.AgentMemberRequestForRun(ctx, run.ID)
	if err != nil || request.SourceMessageID == "" {
		return nil
	}
	return request
}

// publishMentionReply posts the agent's answer under the message that asked
// for it. Failures are posted too, so the asker isn't left waiting.
func (s *SpacesService) publishMentionReply(ctx context.Context, run *db.SpaceRun, request *db.AgentMemberRequest, text string) error {
	actionID, claimed, err := s.database.ClaimRunResponsePublication(ctx, run.ID)
	if err != nil || !claimed {
		return err
	}
	reply := truncateAgentRuntimeText(strings.TrimSpace(text), 4000)
	if run.State != "completed" {
		if reply == "" {
			reply = "I couldn't finish that."
		} else {
			reply = "I couldn't finish that. " + reply
		}
	} else if reply == "" {
		reply = "Done."
	}
	message, publishErr := s.database.CreateAgentMemberRequestMessage(ctx, request, "agent_member_reply", reply)
	details := TestingMustAPIRawJSON(map[string]any{"run_id": run.ID, "request_id": request.ID})
	state := "failed"
	if publishErr == nil {
		state = "completed"
		details = TestingMustAPIRawJSON(map[string]any{"run_id": run.ID, "request_id": request.ID, "message_id": message.ID})
	}
	if finishErr := s.database.FinishRunResponsePublication(ctx, actionID, state, details); finishErr != nil {
		return finishErr
	}
	return publishErr
}

func firstName(name, fallback string) string {
	if fields := strings.Fields(name); len(fields) > 0 {
		return fields[0]
	}
	return fallback
}
