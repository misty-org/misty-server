package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Agents on devices (docs/design/agents-on-devices/BRIEF.md): a request can
// name another of the person's devices to work on. When the agent needs a
// browser there, the device opens a session under its own lease and signs the
// grants for its tab; the asking device continues the conversation with it.
const (
	remoteSessionOp = "browser.session"
	// deviceOnlineWindow matches the claim check (validateDeviceJobTargetTx).
	deviceOnlineWindow = 90 * time.Second
)

// remoteSessionWait: a device that does not open the session by then is
// treated as offline.
var remoteSessionWait = 30 * time.Second

// deviceOfflineError ends the run with words the person can act on. Its
// code is one the runtime treats as ending the run.
type deviceOfflineError struct{ name, reason string }

func (e deviceOfflineError) Error() string {
	return "device_offline: " + e.name + " " + e.reason + " Pick another device and ask again."
}

// remoteScreen is what the asking device needs to continue on the other one.
type remoteScreen struct {
	DeviceID   string                      `json:"deviceId"`
	DeviceName string                      `json:"deviceName"`
	Contexts   []aiInvocationDeviceContext `json:"contexts"`
}

// remoteSessionTarget is the picked device's browser grant that may open a
// session, if the request named another device.
func remoteSessionTarget(contexts []db.AIInvocationContext) *db.AIInvocationContext {
	for index := range contexts {
		item := &contexts[index]
		var capabilities []string
		if item.Kind == "workspace" && item.State == "attached" && json.Unmarshal(item.Capabilities, &capabilities) == nil && containsString(capabilities, remoteSessionOp) {
			return item
		}
	}
	return nil
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

// openRemoteSession asks the picked device to open a browser session for this
// run and returns its tab, signed by that device.
func (s *SpacesService) openRemoteSession(ctx context.Context, record *db.AIInvocationRecord, body aiInvocationInput, target *db.AIInvocationContext, callID, url, reason string) (*remoteScreen, error) {
	name, online := s.deviceDisplayName(ctx, record.UserID, target.DeviceID)
	if !online {
		return nil, deviceOfflineError{name: name, reason: "is offline."}
	}
	agentID := body.AgentID
	if agentID == "" {
		agentID = "misty-unified"
	}
	waitCtx, cancel := context.WithTimeout(ctx, remoteSessionWait)
	defer cancel()
	input := TestingMustAPIRawJSON(map[string]any{"scopeId": target.OpaqueRef, "url": url, "reason": reason, "conversationId": record.ConversationID})
	config := TestingMustAPIRawJSON(map[string]any{"agentId": agentID})
	job, err := s.database.QueueAIInvocationDeviceNodeJob(waitCtx, record.UserID, record.ID, "browser_session_"+callID, 1, target.OpaqueRef, remoteSessionOp, remoteSessionOp, input, config,
		TestingMustAPIRawJSON(map[string]any{"type": "object"}), agentToolObjectOutputSchema())
	if errors.Is(err, db.ErrDeviceNotFound) {
		return nil, deviceOfflineError{name: name, reason: "is not available."}
	}
	if err != nil {
		return nil, err
	}
	current, err := waitForDeviceJob(waitCtx, s.database, record.UserID, job.ID, job.DeadlineAt)
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
			return nil, serveragent.ErrInvalidRequest(name + " does not let agents use its browser. Tell the person to allow it in Settings › Devices on " + name + ", or to pick another device.")
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
	if err := verifyDeviceContextGrants(ctx, s.database, record.UserID, body.AgentID, output.Tabs); err != nil {
		return nil, err
	}
	return &remoteScreen{DeviceID: target.DeviceID, DeviceName: name, Contexts: output.Tabs}, nil
}

// validateDeviceOpenedTabs checks tabs a device opened for agents itself: a
// sub-agent's tab or another device's session. They come from the device,
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

// scopeTaskID is the task whose lease guards a scope: the device's own task
// for another device's session, otherwise the request's task.
func scopeTaskID(body aiInvocationInput, scope string) string {
	if task := scopeContextMetadata(body.DeviceContexts, scope, "task_id"); task != "" {
		return task
	}
	return body.TaskID
}

// remoteScopeDevice names the device of another device's session scope, so
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

// remoteScreenOpen handles screen_open for a request that picked another
// device: it opens a browser session there, or reports why it can't. It
// returns nil, nil when the request works on the asking device.
func (s *SpacesService) remoteScreenOpen(ctx context.Context, record *db.AIInvocationRecord, body aiInvocationInput, callID, url, reason string, desktop bool) (json.RawMessage, error) {
	contexts, err := s.database.AIInvocationContexts(ctx, record.UserID, record.ID)
	if err != nil {
		return nil, err
	}
	target := remoteSessionTarget(contexts)
	if target == nil {
		return nil, nil
	}
	name, _ := s.deviceDisplayName(ctx, record.UserID, target.DeviceID)
	if desktop {
		return nil, serveragent.ErrInvalidRequest("Other apps can be used only on the device the person is asking from. Use a browser on " + name + " (omit target), or tell them to pick this device.")
	}
	for _, item := range contexts {
		var metadata map[string]any
		if item.Kind == "browser_tab" && item.State == "attached" && item.DeviceID == target.DeviceID && json.Unmarshal(item.Metadata, &metadata) == nil && metadata["remote_session"] == true {
			return json.Marshal(map[string]any{"status": "open", "message": "A browser on " + name + " is already attached. Use browser_snapshot with its scopeId " + item.OpaqueRef + "."})
		}
	}
	remote, err := s.openRemoteSession(ctx, record, body, target, callID, url, strings.TrimSpace(reason))
	if err != nil {
		return nil, err
	}
	screen := &screenRequest{Kind: "open", URL: url, Reason: strings.TrimSpace(reason), Location: "remote", Chosen: true, Remote: remote}
	if s.aiInvocations != nil {
		if _, err := s.aiInvocations.restoreDurable(ctx, *record); err != nil {
			return nil, err
		}
		if err := s.aiInvocations.append(record.ID, aiInvocationEvent{Type: "screen.request", ScreenRequest: screen}); err != nil {
			return nil, err
		}
	}
	return json.Marshal(map[string]any{"status": "screen_requested", "kind": "open",
		"message": "Misty opened a browser on " + name + " and will continue this conversation there."})
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
	return s.remoteScreenOpen(ctx, record, body, callID, url, "Fill in the form", false)
}

// TestingSetRemoteSessionWait shortens how long a device has to answer.
func TestingSetRemoteSessionWait(wait time.Duration) func() {
	previous := remoteSessionWait
	remoteSessionWait = wait
	return func() { remoteSessionWait = previous }
}
