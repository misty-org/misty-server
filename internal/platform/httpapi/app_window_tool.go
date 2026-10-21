package api

import (
	"context"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
)

// app.window is the agent's hands on its own Misty window (docs/design/agents-at-cursor/BRIEF.md,
// "Agent windows" and "App commands"): open Misty's own pages as tabs (a Space's home, notes,
// files, chat, the Agents page), list, switch and close its tabs, and switch the window to
// another Space. The first call also hands the agent the window's own pages as a screen for
// the browser tools, so it reads and acts on Misty the way it does on a website.
const appWindowTool = "app.window"

func (s *SpacesService) appWindowRegistration() agenttools.Registration {
	return agenttools.Registration{
		Descriptor: screenDescriptor(appWindowTool,
			"Use your own Misty window as a person would. action open shows a Misty page there as a tab: path "+
				"/spaces/{spaceId} (the Space's home), /spaces/{spaceId}/journal (notes), /spaces/{spaceId}/library (files), "+
				"/spaces/{spaceId}/social (chat) or /agents; websites open with screen_open instead. list_tabs lists the "+
				"window's tabs, switch_tab and close_tab take a tabId from that list, and switch_space moves the window to "+
				"another Space (spaceId from space_list, or personal). The result names the scopeId of Misty's pages in your "+
				"window: read them with browser_snapshot and act with browser_steps and browser_skill, as on a website. "+
				"Settings are not yours to change.",
			map[string]any{
				"action":  map[string]any{"type": "string", "enum": []string{"open", "list_tabs", "switch_tab", "close_tab", "switch_space"}},
				"path":    map[string]any{"type": "string", "maxLength": 300, "description": "For open: the Misty page's path."},
				"tabId":   map[string]any{"type": "string", "maxLength": 200, "description": "For switch_tab and close_tab."},
				"spaceId": map[string]any{"type": "string", "maxLength": 200, "description": "For switch_space: a Space's id, or personal."},
			},
			"action"),
		Handler: s.appWindowHandler,
	}
}

func (s *SpacesService) appWindowHandler(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
	var command struct {
		Action  string `json:"action"`
		Path    string `json:"path,omitempty"`
		TabID   string `json:"tabId,omitempty"`
		SpaceID string `json:"spaceId,omitempty"`
	}
	if json.Unmarshal(request.Arguments, &command) != nil {
		return nil, serveragent.ErrInvalidRequest("Pass an action.")
	}
	switch command.Action {
	case "open":
		if !strings.HasPrefix(command.Path, "/") {
			return nil, serveragent.ErrInvalidRequest("Pass the Misty page's path, such as /spaces/{spaceId}/journal.")
		}
	case "switch_tab", "close_tab":
		if command.TabID == "" {
			return nil, serveragent.ErrInvalidRequest("Pass a tabId from list_tabs.")
		}
	case "switch_space":
		if command.SpaceID == "" {
			return nil, serveragent.ErrInvalidRequest("Pass the Space's id, or personal.")
		}
	case "list_tabs":
	default:
		return nil, serveragent.ErrInvalidRequest("action is open, list_tabs, switch_tab, close_tab or switch_space.")
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
		return nil, serveragent.ErrInvalidRequest("This run has no Misty window to work in; it needs this computer as its device.")
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
	jobInput := TestingMustAPIRawJSON(map[string]any{"scopeId": target.OpaqueRef, "conversationId": record.ConversationID, "appCommand": command})
	job, err := s.database.QueueAIInvocationDeviceNodeJob(waitCtx, record.UserID, record.ID, "app_window_"+request.ID, 1, target.OpaqueRef, remoteSessionOp, remoteSessionOp, jobInput,
		TestingMustAPIRawJSON(map[string]any{"agentId": agentID}), TestingMustAPIRawJSON(map[string]any{"type": "object"}), agentToolObjectOutputSchema())
	if err != nil {
		return nil, err
	}
	current, err := waitForDeviceJob(waitCtx, s.database, record.UserID, job.ID, job.DeadlineAt)
	if err != nil || current.State != "completed" {
		if err != nil {
			_, _ = s.database.StopWorkflowDeviceNodeJob(record.UserID, job.ID)
		}
		return nil, deviceOfflineError{name: name, reason: "did not do that in the agent's window."}
	}
	var output struct {
		Tabs   []aiInvocationDeviceContext `json:"tabs"`
		Result map[string]any              `json:"result"`
	}
	if json.Unmarshal(current.Output, &output) != nil || len(output.Tabs) != 1 {
		return nil, deviceOfflineError{name: name, reason: "returned an invalid answer."}
	}
	view := output.Tabs[0]
	reply := map[string]any{"status": "done", "result": output.Result, "scopeId": view.OpaqueRef}
	// The window's own pages join the run once, as a screen the browser tools act on.
	attached := false
	for _, item := range contexts {
		attached = attached || item.OpaqueRef == view.OpaqueRef
	}
	if !attached {
		if err := validateDeviceOpenedTabs(output.Tabs, target.DeviceID, 1); err != nil {
			return nil, err
		}
		if err := verifyDeviceContextGrants(ctx, s.database, record.UserID, invocation.AgentID, output.Tabs); err != nil {
			return nil, err
		}
		// It works for the window that asked, as a screen the person attached would.
		var body aiInvocationInput
		var metadata map[string]any
		if json.Unmarshal(record.RequestPayload, &body) != nil || json.Unmarshal(view.Metadata, &metadata) != nil {
			return nil, deviceOfflineError{name: name, reason: "returned an invalid answer."}
		}
		metadata["window_label"] = body.WindowLabel
		metadata["app_view"] = true
		view.Metadata, _ = json.Marshal(metadata)
		if _, err := s.database.AttachRunningAIInvocationContext(ctx, record.UserID, record.ID, view.DeviceID, view.Kind, view.OpaqueRef, view.DisplayName, view.Capabilities, view.Metadata, runGrantRecord(view.RunGrant)); err != nil {
			return nil, err
		}
		reply["status"], reply["catalog_changed"] = "open", true
		reply["message"] = "Misty's pages in your window are open to you as scopeId " + view.OpaqueRef + ": read them with browser_snapshot from your next step."
	}
	return json.Marshal(reply)
}
