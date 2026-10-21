package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// The agent's own browser session (docs/plans/odysseys-campaign.md): when the
// agent needs a browser, the computer the person sent the request from opens a
// hidden tab of the agent's own under its own lease and signs the grant for it,
// and the tab joins the run. An agent run works only on the device it was sent
// from; no request names another device (docs/design/agent-browser/BRIEF.md).
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
// docs/plans/odysseys-campaign.md). A workspace grant for any other device is
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
	// Where opening a page spends its time, one line per call (docs/plans/agent-harness-v2.md, change 5).
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
	input := TestingMustAPIRawJSON(map[string]any{"scopeId": target.OpaqueRef, "url": url, "reason": reason, "conversationId": record.ConversationID, "newTab": newTab})
	config := TestingMustAPIRawJSON(map[string]any{"agentId": agentID})
	job, err := s.database.QueueAIInvocationDeviceNodeJob(waitCtx, record.UserID, record.ID, "browser_session_"+callID, 1, target.OpaqueRef, remoteSessionOp, remoteSessionOp, input, config,
		TestingMustAPIRawJSON(map[string]any{"type": "object"}), agentToolObjectOutputSchema())
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

// remoteScreenOpen handles screen_open with a browser session on the sending
// computer, unless the agent asked for the person's own page or other apps.
// The computer opens the agent's own tab, and the tab joins this run, so the
// agent keeps working in the same run with browser tools from its next step
// instead of handing off to a new run. It returns nil, nil when no session applies.
func (s *SpacesService) remoteScreenOpen(ctx context.Context, record *db.AIInvocationRecord, body aiInvocationInput, callID, url, reason string, desktop bool, place string) (json.RawMessage, error) {
	// Calls made together (one per site) take turns, so each sees the tabs the one before
	// it opened and gets its own instead of racing for the first.
	lock := &screenOpenLocks[fnv32(record.ID)%uint32(len(screenOpenLocks))]
	lock.Lock()
	defer lock.Unlock()
	contexts, err := s.database.AIInvocationContexts(ctx, record.UserID, record.ID)
	if err != nil {
		return nil, err
	}
	// Only acting in the person's own page (current_tab) or other apps (desktop) hands off;
	// a new tab or window becomes a tab of the agent's own, in this run.
	if place == "current_tab" || desktop {
		return nil, nil
	}
	target := sessionTarget(contexts)
	if target == nil {
		return nil, nil
	}
	name := "this computer"
	programs := contextAllows(target, browserRunTool)
	// The agent's own tabs on that device in this run. A call with a url opens another
	// one beside them (up to maxOwnTabs), so pages the person asked to keep open stay
	// open; a call without one goes back to the first.
	own := []string{}
	already := ""
	for _, item := range contexts {
		var metadata map[string]any
		if item.Kind == "browser_tab" && item.State == "attached" && item.DeviceID == target.DeviceID && json.Unmarshal(item.Metadata, &metadata) == nil && metadata["remote_session"] == true {
			own = append(own, item.OpaqueRef)
			// A page opened again goes back to its tab rather than a duplicate of it.
			if url != "" && metadata["opened_url"] == url && already == "" {
				already = item.OpaqueRef
			}
		}
	}
	if already != "" {
		return sessionOpened(already, name, url, true, programs, own...)
	}
	if len(own) > 0 && (url == "" || len(own) >= maxOwnTabs) {
		return sessionOpened(own[0], name, url, true, programs, own...)
	}
	remote, err := s.openRemoteSession(ctx, record, body, target, callID, url, strings.TrimSpace(reason), len(own) > 0)
	if err != nil {
		return nil, err
	}
	tab := remote.Contexts[0]
	// The tab works for the window that asked, as a screen the person attached would.
	var metadata map[string]any
	if json.Unmarshal(tab.Metadata, &metadata) != nil {
		return nil, deviceOfflineError{name: name, reason: "returned an invalid browser session."}
	}
	metadata["window_label"] = body.WindowLabel
	if url != "" {
		metadata["opened_url"] = url
	}
	tab.Metadata, _ = json.Marshal(metadata)
	if _, err := s.database.AttachRunningAIInvocationContext(ctx, record.UserID, record.ID, tab.DeviceID, tab.Kind, tab.OpaqueRef, tab.DisplayName, tab.Capabilities, tab.Metadata, runGrantRecord(tab.RunGrant)); err != nil {
		// Calls made together (one per site) share the device's one session tab: the
		// first attaches it and the others find it attached.
		if attached, readErr := s.database.AIInvocationContexts(ctx, record.UserID, record.ID); readErr == nil {
			for _, item := range attached {
				if item.OpaqueRef == tab.OpaqueRef {
					return sessionOpened(tab.OpaqueRef, name, url, true, programs, append(own, tab.OpaqueRef)...)
				}
			}
		}
		return nil, err
	}
	return sessionOpened(tab.OpaqueRef, name, url, false, programs, append(own, tab.OpaqueRef)...)
}

// screenOpenLocks serialize screen_open per run (striped, so nothing accumulates).
var screenOpenLocks [64]sync.Mutex

func fnv32(text string) uint32 {
	hash := uint32(2166136261)
	for index := 0; index < len(text); index++ {
		hash = (hash ^ uint32(text[index])) * 16777619
	}
	return hash
}

// maxOwnTabs is how many tabs of its own a run may keep open on one device.
const maxOwnTabs = 32

// sessionOpened tells the agent which of its own tabs it is in, and lists every one.
func sessionOpened(scopeID, name, url string, reused, programs bool, tabs ...string) (json.RawMessage, error) {
	opened := "Your own browser tab is open on " + name
	if reused {
		opened = "Your own browser tab on " + name + " is already open"
	}
	// The runtime adds the new page's snapshot right after this result (agent-runtime src/opened-pages.ts).
	next := "Its page snapshot follows, so act on it next"
	if url == "" || reused {
		next = "Go to pages in it with browser_navigate, which returns each page's snapshot"
	}
	if len(tabs) == 0 {
		tabs = []string{scopeID}
	}
	if programs {
		// With browser_run the page is worked in programs (docs/plans/agent-harness-v3.md): no
		// snapshot follows, and the message points there rather than at the step tools.
		return json.Marshal(map[string]any{"status": "open", "scopeId": scopeID, "tabs": tabs, "catalog_changed": true,
			"message": opened + " (scopeId " + scopeID + "). Work it with browser_run: getTab(\"" + scopeID + "\") gives this tab in a program, openTab(url) opens another. " +
				"Your open tabs: " + strings.Join(tabs, ", ") + "."})
	}
	return json.Marshal(map[string]any{"status": "open", "scopeId": scopeID, "tabs": tabs, "catalog_changed": true,
		"message": opened + " (scopeId " + scopeID + "). Browser tools act on it from your next step; pass its scopeId. " + next + ", and keep working in this run. " +
			"Your open tabs: " + strings.Join(tabs, ", ") + ". Open a new tab (screen_open with its url, up to 32) only for a page the person wants kept open; read other pages by navigating a tab you have."})
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
