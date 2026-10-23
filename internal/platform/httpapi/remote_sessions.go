package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

// The agent's own browser session (docs/agents/evals/odysseys.md): when the
// agent needs a browser, the computer the person sent the request from opens a
// hidden tab of the agent's own under its own lease and signs the grant for it,
// and the tab joins the run. An agent run works only on the device it was sent
// from; no request names another device (docs/agents/browser.md).
const (
	remoteSessionOp = "browser.session"
	// deviceOnlineWindow matches the claim check (validateDeviceJobTargetTx).
	deviceOnlineWindow = 90 * time.Second
)

// remoteSessionWait: a computer that does not open the session by then is
// treated as offline.
var remoteSessionWait = 30 * time.Second

// deviceOfflineError ends the run with words the person can act on. Its
// code is one the runtime treats as ending the run.
type deviceOfflineError struct{ name, reason string }

func (e deviceOfflineError) Error() string {
	return "device_offline: " + e.name + " " + e.reason + " Ask again once Misty is open there."
}

// remoteScreen is the agent's own tab a computer opened, with the grant it signed.
type remoteScreen struct {
	DeviceID   string                      `json:"deviceId"`
	DeviceName string                      `json:"deviceName"`
	Contexts   []aiInvocationDeviceContext `json:"contexts"`
}

// sessionTarget is the workspace grant a browser session opens under: the
// sending computer's own (the agent's own hidden tab,
// docs/agents/evals/odysseys.md). A workspace grant for any other device is
// never used.
func sessionTarget(contexts []db.AIInvocationContext) *db.AIInvocationContext {
	for index := range contexts {
		item := &contexts[index]
		var capabilities []string
		if item.Kind != "workspace" || item.State != "attached" || !fromThisComputer(item.Metadata) || json.Unmarshal(item.Capabilities, &capabilities) != nil || !containsString(capabilities, remoteSessionOp) {
			continue
		}
		return item
	}
	return nil
}

// fromThisComputer reports a context the asking desktop sent for itself.
func fromThisComputer(metadata json.RawMessage) bool {
	var fields map[string]any
	return json.Unmarshal(metadata, &fields) == nil && fields["source"] == "desktop"
}

func (s *SpacesService) deviceDisplayName(ctx context.Context, userID, deviceID string) (string, bool) {
	device, err := s.database.AccountDevice(ctx, userID, deviceID)
	if err != nil || device.RevokedAt != nil {
		return "That device", false
	}
	name := strings.TrimSpace(device.Name)
	if name == "" {
		name = "That device"
	}
	return name, time.Since(device.LastSeenAt) < deviceOnlineWindow
}

// openRemoteSession asks the sending computer to open a browser session for
// this run and returns its tab, signed by that computer.
func (s *SpacesService) openRemoteSession(ctx context.Context, record *db.AIInvocationRecord, body aiInvocationInput, target *db.AIInvocationContext, callID, url, reason string, newTab bool) (*remoteScreen, error) {
	// Where opening a page spends its time, one line per call (docs/agents/harness.md, Device jobs).
	timing := newPhaseTimer("screen_open_timing", record.ID)
	defer timing.log()
	name, online := s.deviceDisplayName(ctx, record.UserID, target.DeviceID)
	timing.mark("device")
	if !online {
		return nil, deviceOfflineError{name: name, reason: "is offline."}
	}
	agentID := body.AgentID
	if agentID == "" {
		agentID = "misty-unified"
	}
	waitCtx, cancel := context.WithTimeout(ctx, remoteSessionWait)
	defer cancel()
	input := MustAPIRawJSON(map[string]any{"scopeId": target.OpaqueRef, "url": url, "reason": reason, "conversationId": record.ConversationID, "newTab": newTab})
	config := MustAPIRawJSON(map[string]any{"agentId": agentID})
	job, err := s.database.QueueAIInvocationDeviceNodeJob(waitCtx, record.UserID, record.ID, "browser_session_"+callID, 1, target.OpaqueRef, remoteSessionOp, remoteSessionOp, input, config,
		MustAPIRawJSON(map[string]any{"type": "object"}), agentToolObjectOutputSchema())
	if errors.Is(err, db.ErrDeviceNotFound) {
		return nil, deviceOfflineError{name: name, reason: "is not available."}
	}
	if err != nil {
		return nil, err
	}
	timing.mark("queued")
	current, err := waitForDeviceJob(waitCtx, s.database, record.UserID, job.ID, job.DeadlineAt)
	timing.mark("device_job")
	if err != nil {
		_, _ = s.database.StopWorkflowDeviceNodeJob(record.UserID, job.ID)
		return nil, deviceOfflineError{name: name, reason: "did not answer."}
	}
	switch current.State {
	case "completed":
	case "failed", "uncertain":
		if strings.Contains(current.ErrorCode, "agent_busy") {
			return nil, deviceOfflineError{name: name, reason: "is busy with another task for this agent."}
		}
		if strings.Contains(current.ErrorCode, "device_grant_invalid") {
			return nil, deviceOfflineError{name: name, reason: "refused the browser session."}
		}
		return nil, deviceOfflineError{name: name, reason: "could not open a browser."}
	default:
		return nil, deviceOfflineError{name: name, reason: "did not answer."}
	}
	var output struct {
		Tabs []aiInvocationDeviceContext `json:"tabs"`
	}
	if json.Unmarshal(current.Output, &output) != nil {
		return nil, deviceOfflineError{name: name, reason: "returned an invalid browser session."}
	}
	if err := validateDeviceOpenedTabs(output.Tabs, target.DeviceID, 1); err != nil {
		return nil, err
	}
	timing.mark("validated")
	if err := verifyDeviceContextGrants(ctx, s.database, record.UserID, body.AgentID, output.Tabs); err != nil {
		return nil, err
	}
	timing.mark("grants")
	return &remoteScreen{DeviceID: target.DeviceID, DeviceName: name, Contexts: output.Tabs}, nil
}

// validateDeviceOpenedTabs checks tabs a device opened for agents itself: a
// sub-agent's tab or the agent's own session. They come from the device,
// never from page content, so no page reference applies; each must be a
// browser tab on that device with browser-only capabilities and its own grant.
func validateDeviceOpenedTabs(tabs []aiInvocationDeviceContext, deviceID string, count int) error {
	if len(tabs) != count || deviceID == "" {
		return errors.New("the device opened the wrong number of tabs")
	}
	seen := map[string]bool{}
	for _, tab := range tabs {
		ref := strings.TrimSpace(tab.OpaqueRef)
		if tab.DeviceID != deviceID || tab.Kind != "browser_tab" || ref == "" || len(ref) > 256 || seen[ref] || tab.RunGrant == nil {
			return errors.New("a tab the device opened is invalid")
		}
		seen[ref] = true
		if !db.DeviceContextCapabilitiesAllowed("browser_tab", tab.Capabilities) {
			return errors.New("a tab the device opened asks for capabilities it cannot have")
		}
		var metadata map[string]any
		if json.Unmarshal(tab.Metadata, &metadata) != nil || metadata["app_id"] != "browser" {
			return errors.New("a tab the device opened is invalid")
		}
	}
	return nil
}

// scopeContextMetadata reads one field of the attached context for a scope.
func scopeContextMetadata(contexts []aiInvocationDeviceContext, scope, field string) string {
	for _, item := range contexts {
		if item.OpaqueRef != scope {
			continue
		}
		var metadata map[string]any
		if json.Unmarshal(item.Metadata, &metadata) == nil {
			value, _ := metadata[field].(string)
			return value
		}
	}
	return ""
}

// scopeTaskID is the task whose lease guards a scope: the session's own task
// for the agent's own tab, otherwise the request's task.
func scopeTaskID(body aiInvocationInput, scope string) string {
	if task := scopeContextMetadata(body.DeviceContexts, scope, "task_id"); task != "" {
		return task
	}
	return body.TaskID
}

// remoteScopeDevice names the device of an agent's own session scope, so
// losing it ends the step instead of waiting.
func (s *SpacesService) remoteScopeDevice(ctx context.Context, userID string, body aiInvocationInput, scope string) (string, bool) {
	for _, item := range body.DeviceContexts {
		var metadata map[string]any
		if item.OpaqueRef == scope && json.Unmarshal(item.Metadata, &metadata) == nil && metadata["remote_session"] == true {
			name, _ := s.deviceDisplayName(ctx, userID, item.DeviceID)
			return name, true
		}
	}
	return "", false
}

// TestingOpenRemoteSession runs screen_open for a stored request.
func (s *SpacesService) TestingOpenRemoteSession(ctx context.Context, userID, invocationID, callID, url string) (json.RawMessage, error) {
	record, err := s.database.AIInvocationByID(ctx, userID, invocationID)
	if err != nil {
		return nil, err
	}
	var body aiInvocationInput
	if json.Unmarshal(record.RequestPayload, &body) != nil {
		return nil, db.ErrSpaceInvalid
	}
	return s.remoteScreenOpen(ctx, record, body, callID, url, "Fill in the form", false, "")
}

// TestingSetRemoteSessionWait shortens how long a device has to answer.
func TestingSetRemoteSessionWait(wait time.Duration) func() {
	previous := remoteSessionWait
	remoteSessionWait = wait
	return func() { remoteSessionWait = previous }
}
