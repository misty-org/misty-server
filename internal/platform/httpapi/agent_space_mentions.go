package api

import (
	"context"
	"errors"
	"log"
	"slices"
	"strings"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

// startAgentMentionRequests turns @mentions of agent members in a Space chat
// message into requests to those agents. Each starts at once and runs on the
// sender's models.
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
		_, err := s.database.CreateAgentMemberRequest(ctx, db.AgentMemberRequestInput{
			SpaceID: message.SpaceID, RequesterUserID: userID, RequesterRunID: db.MentionRequester(message.ID),
			TargetAgentID: agentID, Message: text, IdempotencyKey: agentID, SourceConversationID: message.ConversationID,
		})
		if err != nil {
			s.explainUnstartedMention(ctx, userID, message, agentID, err)
		}
	}
}

// explainUnstartedMention says in the chat why a mentioned agent won't answer
// when it is busy or the asker hit a limit. Mentions of agents that aren't in
// this Space are ordinary text.
func (s *SpacesService) explainUnstartedMention(ctx context.Context, userID string, message *db.SpaceMessage, agentID string, cause error) {
	reason := ""
	switch {
	case errors.Is(cause, db.ErrAgentRequestBusy):
		reason = "I have too many requests open. Try again when some finish."
	case errors.Is(cause, db.ErrAgentRequestLimit):
		reason = "You've sent a lot of requests in the last hour. Try again later."
	default:
		log.Printf("agent mention: request to %s failed: %v", agentID, cause)
		return
	}
	agents, err := s.database.SpaceAgents(ctx, userID, message.SpaceID)
	if err != nil {
		return
	}
	for _, agent := range agents {
		if agent.AgentID != agentID {
			continue
		}
		request := &db.AgentMemberRequest{SpaceID: message.SpaceID, TargetAgentID: agentID, TargetOwnerUserID: agent.OwnerUserID,
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
	details := MustAPIRawJSON(map[string]any{"run_id": run.ID, "request_id": request.ID})
	state := "failed"
	if publishErr == nil {
		state = "completed"
		details = MustAPIRawJSON(map[string]any{"run_id": run.ID, "request_id": request.ID, "message_id": message.ID})
	}
	if finishErr := s.database.FinishRunResponsePublication(ctx, actionID, state, details); finishErr != nil {
		return finishErr
	}
	if publishErr == nil && run.State == "completed" {
		s.startAgentReplyMentions(ctx, request, message, reply)
	}
	return publishErr
}

// startAgentReplyMentions lets agents talk to each other in the chat: when a
// reply @mentions another agent in the Space, that agent picks the thread up,
// visibly and on the models of the person who asked first. It goes one hop deep.
func (s *SpacesService) startAgentReplyMentions(ctx context.Context, request *db.AgentMemberRequest, message *db.SpaceMessage, reply string) {
	agents, err := s.database.SpaceAgents(ctx, request.RequesterUserID, request.SpaceID)
	if err != nil {
		return
	}
	lower := strings.ToLower(reply)
	started := 0
	for _, agent := range agents {
		if started >= 3 || agent.AgentID == request.TargetAgentID || !strings.Contains(lower, "@"+strings.ToLower(agent.AgentName)) {
			continue
		}
		started++
		if _, err := s.database.CreateAgentMemberRequest(ctx, db.AgentMemberRequestInput{
			SpaceID: request.SpaceID, RequesterUserID: request.RequesterUserID, RequesterAgentID: request.TargetAgentID,
			RequesterRunID: db.ReplyRequester(message.ID), TargetAgentID: agent.AgentID, Message: reply,
			IdempotencyKey: agent.AgentID, SourceConversationID: request.SourceConversationID,
		}); err != nil && !errors.Is(err, db.ErrAgentRequestLimit) {
			log.Printf("agent reply mention: request to %s failed: %v", agent.AgentID, err)
		}
	}
}
