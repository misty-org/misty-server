package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// browser.run (docs/agents/harness.md, browser_run): the agent writes a short program against
// a page API, and the computer the run was sent from runs it in a sandbox against the agent's own
// tabs. One call can open pages, act on them and read many items, where a step tool takes a turn
// for each. Every page call is the device operation a step tool would run, under the run's grant
// and the same confirm rules; tabs the program opens join the run as screen_open's do.
const (
	browserRunTool = "browser.run"
	// A program has two minutes on the device, plus time to queue and report.
	browserRunWait = 170 * time.Second
	maxProgramCode = 20_000
)

const browserRunDescription = "Run a short JavaScript program in your own browser tabs, the fastest way to do several page steps in one call. " +
	"The program body runs inside an async function: await page calls and return what you found (2 minutes, 80 page calls). " +
	"openTab(url) opens a new tab of yours and waits for it to load; listTabs() lists your tabs; getTab(idOrUrlPart) finds one. On a tab: " +
	"read({maxChars}) gives {url, title, tree} with a ref on each control; text({maxChars}) gives the page as markdown; find(words) gives [{ref, line}]; " +
	"goto(url); click(target); type(target, text, {submit: true}) types like a person (search boxes, suggestions); fill(target, text); select(target, value); " +
	"check(target); hover(target); press(key, target); scroll(\"down\"); scrollTo(text) brings text into view; waitFor({text | textGone | urlIncludes, timeoutMs}); keep() marks a page the person asked to keep open. " +
	"openTab, getTab and listTabs are async: await them. A page left open as proof is pictured as it stands at the end, so scrollTo the text that shows what you report first. " +
	"A target is a ref from read() such as \"e12\", a locator {role: \"button\", name: \"Search\"}, or words such as \"Search button\". " +
	"note(object) records a finding; log(...) prints. A failed step throws with the real reason: catch it or let the program end there. " +
	"Example: const t = await openTab(\"https://example.com\"); await t.type({role: \"searchbox\", name: \"\"}, \"joggers\", {submit: true}); " +
	"await t.waitFor({text: \"results\"}); return (await t.text({maxChars: 6000})).text; " +
	"A video is a source too (a tutorial's steps are in it): open its page, try { await t.click(\"Show transcript\"); } catch (e) {}, then read t.text() for the title, description and spoken steps, and summarize them."

func (s *SpacesService) browserRunRegistration() agenttools.Registration {
	descriptor := screenDescriptor(browserRunTool, browserRunDescription,
		map[string]any{"code": map[string]any{"type": "string", "minLength": 1, "maxLength": maxProgramCode, "description": "The program body (JavaScript)."}},
		"code")
	descriptor.Risk = agent.RiskWrite
	descriptor.Idempotent = false
	return agenttools.Registration{Descriptor: descriptor, Handler: s.browserRunHandler}
}

func (s *SpacesService) browserRunHandler(ctx context.Context, invocation agenttools.Invocation, request agent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(request.Arguments, &input) != nil || strings.TrimSpace(input.Code) == "" || len(input.Code) > maxProgramCode {
		return nil, agent.ErrInvalidRequest("Pass the program as code (1–20000 characters).")
	}
	record, err := s.database.AIInvocationByID(ctx, invocation.UserID, invocation.RunID)
	if err != nil {
		return nil, err
	}
	contexts, err := s.database.AIInvocationContexts(ctx, record.UserID, record.ID)
	if err != nil {
		return nil, err
	}
	target := sessionTarget(contexts)
	if target == nil || !contextAllows(target, browserRunTool) {
		return nil, agent.ErrInvalidRequest("This run has no browser of its own on this computer, so browser_run is not available; use screen_open and the page tools.")
	}
	name, online := s.deviceDisplayName(ctx, record.UserID, target.DeviceID)
	if !online {
		return nil, deviceOfflineError{name: name, reason: "is offline."}
	}
	var body aiInvocationInput
	_ = json.Unmarshal(record.RequestPayload, &body)
	config := s.browserToolConfig(ctx, invocation)
	if body.AgentID != "" {
		config["agentId"] = body.AgentID
	}
	waitCtx, cancel := context.WithTimeout(ctx, browserRunWait)
	defer cancel()
	jobInput := MustAPIRawJSON(map[string]any{"scopeId": target.OpaqueRef, "conversationId": record.ConversationID, "code": input.Code})
	job, err := s.database.QueueAIInvocationDeviceNodeJob(waitCtx, record.UserID, record.ID, "browser_run_"+request.ID, 1, target.OpaqueRef, browserRunTool, browserRunTool, jobInput,
		MustAPIRawJSON(config), MustAPIRawJSON(map[string]any{"type": "object"}), agentToolObjectOutputSchema())
	if err != nil {
		return nil, err
	}
	current, err := waitForDeviceJob(waitCtx, s.database, record.UserID, job.ID, job.DeadlineAt)
	if err != nil {
		_, _ = s.database.StopWorkflowDeviceNodeJob(record.UserID, job.ID)
		return nil, deviceOfflineError{name: name, reason: "did not finish the program."}
	}
	if current.State != "completed" {
		reason := strings.TrimPrefix(strings.TrimSpace(current.ErrorCode), "device_execution_uncertain:")
		if reason == "" {
			reason = current.State
		}
		return json.Marshal(map[string]any{"ok": false, "error": "The program did not run to the end on the device (" + reason + "). What it did before stopping may have happened; read your tabs before running it again."})
	}
	var output map[string]any
	if json.Unmarshal(current.Output, &output) != nil {
		return nil, deviceOfflineError{name: name, reason: "returned an invalid program result."}
	}
	opened, _ := json.Marshal(output["opened"])
	delete(output, "opened")
	var tabs []aiInvocationDeviceContext
	if json.Unmarshal(opened, &tabs) == nil && len(tabs) > 0 {
		if err := s.attachProgramTabs(ctx, record, body, target.DeviceID, tabs); err != nil {
			output["tabsNote"] = "Some tabs the program opened could not join the run: " + err.Error()
		}
		output["catalog_changed"] = true
	}
	return json.Marshal(output)
}

// attachProgramTabs joins tabs a program opened to the run, checked as screen_open checks the tab
// a session opens: on that device, browser-only, each with the grant the device signed.
func (s *SpacesService) attachProgramTabs(ctx context.Context, record *db.AIInvocationRecord, body aiInvocationInput, deviceID string, tabs []aiInvocationDeviceContext) error {
	lock := &screenOpenLocks[fnv32(record.ID)%uint32(len(screenOpenLocks))]
	lock.Lock()
	defer lock.Unlock()
	if err := validateDeviceOpenedTabs(tabs, deviceID, len(tabs)); err != nil {
		return err
	}
	if err := verifyDeviceContextGrants(ctx, s.database, record.UserID, body.AgentID, tabs); err != nil {
		return err
	}
	attached, err := s.database.AIInvocationContexts(ctx, record.UserID, record.ID)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, item := range attached {
		known[item.OpaqueRef] = true
	}
	for _, tab := range tabs {
		if known[tab.OpaqueRef] {
			continue
		}
		var metadata map[string]any
		_ = json.Unmarshal(tab.Metadata, &metadata)
		metadata["window_label"] = body.WindowLabel
		tab.Metadata, _ = json.Marshal(metadata)
		if _, err := s.database.AttachRunningAIInvocationContext(ctx, record.UserID, record.ID, tab.DeviceID, tab.Kind, tab.OpaqueRef, tab.DisplayName, tab.Capabilities, tab.Metadata, runGrantRecord(tab.RunGrant)); err != nil {
			return err
		}
	}
	return nil
}

// contextAllows reports whether an attached context's grant lists a capability.
func contextAllows(item *db.AIInvocationContext, capability string) bool {
	var capabilities []string
	return json.Unmarshal(item.Capabilities, &capabilities) == nil && containsString(capabilities, capability)
}
