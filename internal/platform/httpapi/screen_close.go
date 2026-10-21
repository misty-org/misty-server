package api

import (
	"context"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// screen.close closes one of the agent's own extra tabs: a sign-in page, a duplicate or a
// page the person did not ask to keep, so the tabs left open at the end are the ones the
// request named (docs/plans/odysseys-campaign.md). The run's first tab stays; it is the
// session's, and the agent navigates it instead.
const screenCloseTool = "screen.close"

func (s *SpacesService) screenCloseRegistration() agenttools.Registration {
	return agenttools.Registration{
		Descriptor: screenDescriptor(screenCloseTool,
			"Close one of your own browser tabs by its scopeId when the person does not need it kept open, such as a sign-in page, "+
				"a duplicate or a page you only read, so the tabs left open are the ones the request asked for. Your first tab stays "+
				"open; navigate it instead. Never close a tab the person asked to keep.",
			map[string]any{"scopeId": map[string]any{"type": "string", "minLength": 8, "maxLength": 256, "description": "The tab to close, from screen_open's list of your tabs."}},
			"scopeId"),
		Handler: s.screenCloseHandler,
	}
}

func (s *SpacesService) screenCloseHandler(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		ScopeID string `json:"scopeId"`
	}
	if json.Unmarshal(request.Arguments, &input) != nil || strings.TrimSpace(input.ScopeID) == "" {
		return nil, serveragent.ErrInvalidRequest("Pass the scopeId of the tab to close.")
	}
	record, err := s.database.AIInvocationByID(ctx, invocation.UserID, invocation.RunID)
	if err != nil {
		return nil, err
	}
	lock := &screenOpenLocks[fnv32(record.ID)%uint32(len(screenOpenLocks))]
	lock.Lock()
	defer lock.Unlock()
	contexts, err := s.database.AIInvocationContexts(ctx, record.UserID, record.ID)
	if err != nil {
		return nil, err
	}
	target := sessionTarget(contexts)
	if target == nil {
		return nil, serveragent.ErrInvalidRequest("This run has no tabs of its own to close.")
	}
	own := ownTabs(contexts, target.DeviceID)
	index := -1
	for position, scope := range own {
		if scope == input.ScopeID {
			index = position
		}
	}
	switch {
	case index < 0:
		return nil, serveragent.ErrInvalidRequest("That is not one of your own tabs. Your tabs: " + strings.Join(own, ", ") + ".")
	case index == 0:
		return nil, serveragent.ErrInvalidRequest("Your first tab stays open; navigate it to a page the person wants instead.")
	}
	name, online := s.deviceDisplayName(ctx, record.UserID, target.DeviceID)
	if !online {
		return nil, deviceOfflineError{name: name, reason: "is offline."}
	}
	agentID := invocation.AgentID
	if agentID == "" {
		agentID = "misty-unified"
	}
	waitCtx, cancel := context.WithTimeout(ctx, remoteSessionWait)
	defer cancel()
	jobInput := TestingMustAPIRawJSON(map[string]any{"scopeId": target.OpaqueRef, "conversationId": record.ConversationID, "closeScope": input.ScopeID})
	config := TestingMustAPIRawJSON(map[string]any{"agentId": agentID})
	job, err := s.database.QueueAIInvocationDeviceNodeJob(waitCtx, record.UserID, record.ID, "browser_close_"+request.ID, 1, target.OpaqueRef, remoteSessionOp, remoteSessionOp, jobInput, config,
		TestingMustAPIRawJSON(map[string]any{"type": "object"}), agentToolObjectOutputSchema())
	if err != nil {
		return nil, err
	}
	current, err := waitForDeviceJob(waitCtx, s.database, record.UserID, job.ID, job.DeadlineAt)
	if err != nil || current.State != "completed" {
		if err != nil {
			_, _ = s.database.StopWorkflowDeviceNodeJob(record.UserID, job.ID)
		}
		return nil, deviceOfflineError{name: name, reason: "did not close the tab."}
	}
	if _, err := s.database.DetachAIInvocationContext(ctx, record.UserID, record.ID, input.ScopeID); err != nil {
		return nil, err
	}
	left := append(append([]string{}, own[:index]...), own[index+1:]...)
	return json.Marshal(map[string]any{"status": "closed", "message": "Closed that tab. Your open tabs: " + strings.Join(left, ", ") + "."})
}

// ownTabs lists the agent's own tabs on a device in this run, the first opened first.
func ownTabs(contexts []db.AIInvocationContext, deviceID string) []string {
	own := []string{}
	for _, item := range contexts {
		var metadata map[string]any
		if item.Kind == "browser_tab" && item.State == "attached" && item.DeviceID == deviceID && json.Unmarshal(item.Metadata, &metadata) == nil && metadata["remote_session"] == true {
			own = append(own, item.OpaqueRef)
		}
	}
	return own
}
