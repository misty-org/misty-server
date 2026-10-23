package api

import (
	"encoding/json"
	"errors"
	"log"
	"regexp"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/workflow"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpStructuredResult(raw json.RawMessage) *mcp.CallToolResult {
	var structured any
	if len(raw) == 0 || json.Unmarshal(raw, &structured) != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "The action returned no valid result. Its completion is unconfirmed."}}}
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(raw)}},
		StructuredContent: structured,
	}
}

func mcpToolError(err error) *mcp.CallToolResult {
	if errors.Is(err, db.ErrAgentToolboxActionUnknown) {
		return mcpStructuredResult(json.RawMessage(`{"status":"uncertain","reason":"The action may have completed. Reconcile its outcome before retrying."}`))
	}
	if errors.Is(err, errWorkspaceSnapshotStale) {
		return mcpStructuredResult(json.RawMessage(`{"status":"failure","reason":"browser_snapshot_stale","attempted":false,"message":"The screenshot was consumed or became stale. No input was dispatched. Call browser_workspace_visual to capture the current control surface, then decide the next action with its new documentId. Capture again after every action, including clicking before typing."}`))
	}
	if errors.Is(err, errBrowserSnapshotStale) {
		return mcpStructuredResult(json.RawMessage(`{"status":"failure","reason":"browser_snapshot_stale","attempted":false,"message":"The page changed since it was read. No action was dispatched. Read it again with browser_snapshot (or browser_inspect, if that is what you used) and use its new references before deciding the next action."}`))
	}
	message := "Misty could not complete this tool call."
	var invalid agent.ErrInvalidRequest
	var offline deviceOfflineError
	if errors.As(err, &offline) {
		message = offline.Error()
	} else if errors.Is(err, errBrowserObservationFailed) {
		message = "browser_observation_failed: The page could not be observed; the agent window may be paused or closed. Observe it again once; if that also fails, report the browser problem."
	} else if errors.Is(err, errBrowserWebviewUnavailable) {
		// Most often an agent's tab whose address never loaded (a wrong domain): nothing ran.
		message = "browser_webview_unavailable: This tab has no page that can be read or used; its address may not exist or failed to load. Nothing was done. Navigate the tab to an address that works (search for the site if unsure), or close it and open another tab."
	} else if errors.Is(err, errDesktopAccessibilityRequired) {
		message = "desktop_accessibility_required: Allow the running Misty app in System Settings → Privacy & Security → Accessibility, then retry."
	} else if errors.Is(err, errDesktopScreenRecordingRequired) {
		message = "desktop_screen_recording_required: Allow the running Misty app in System Settings → Privacy & Security → Screen Recording, then retry."
	} else if errors.Is(err, db.ErrAgentExecutionTimeLimit) {
		message = "agent_execution_time_limit: This run used its execution-time allowance. Review completed work before starting another request."
	} else if errors.Is(err, agenttools.ErrCapabilityDenied) || errors.Is(err, agenttools.ErrToolNotFound) || errors.Is(err, workflow.ErrCapabilityDenied) || errors.Is(err, db.ErrSpaceForbidden) {
		message = "This tool call is not allowed for the current run."
	} else if errors.As(err, &invalid) {
		// Validation messages tell the model how to correct its call.
		message = truncateAgentRuntimeText(string(invalid), 500)
	} else if errors.Is(err, agenttools.ErrArgumentsInvalid) {
		message = "The arguments do not match this tool's input schema."
	} else if errors.Is(err, db.ErrSpaceInvalid) {
		message = "The tool arguments are invalid."
	} else if code := deviceFailureCode(err.Error()); code != "" {
		// The device's own failure category is safe to show and tells the agent what to do next
		// (docs/agents/harness.md, Errors and budget); "could not complete" told it nothing.
		message = deviceFailureMessage(code)
	} else if strings.Contains(err.Error(), "drawing_conflict") {
		message = "The drawing changed since it was read. Call drawing_read again, then retry with its latest base_hash."
	} else if strings.Contains(err.Error(), "document_too_large") {
		message = "The drawing would exceed the collaboration document size limit. Apply a smaller scene or delete unused elements first."
	}
	if message == "Misty could not complete this tool call." {
		// The model sees a generic failure; the reason belongs in the server log.
		log.Printf("agent tool call failed: %v", err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: message}}, IsError: true}
}

var deviceFailurePattern = regexp.MustCompile(`browser device tool failed: ([a-z_:]{3,80})`)

// deviceFailureCode is the category a device reported for a failed browser step, or "".
func deviceFailureCode(text string) string {
	if match := deviceFailurePattern.FindStringSubmatch(text); match != nil {
		return match[1]
	}
	return ""
}

// deviceFailureMessage names a device failure and the next step that usually works.
func deviceFailureMessage(code string) string {
	hints := map[string]string{
		"browser_timeout":          "The page did not answer in time. Read it again, or wait for it (waitFor) before acting.",
		"browser_tab_closed":       "That tab is closed. List your tabs, or open the page again.",
		"agent_tab_closed":         "That tab is closed. List your tabs, or open the page again.",
		"browser_grant_invalid":    "This run may no longer act in that tab. Open the page in a new tab of your own.",
		"invalid_scope":            "That scopeId is not one of your tabs. Use one from your list of tabs.",
		"browser_snapshot_stale":   "The page changed since it was read. Read it again and use the new refs.",
		"destination_unreachable":  "The site did not answer. Check the address, or search for the site.",
		"destination_refused":      "The site refused the connection. Try another page of the site or another source.",
		"fetch_failed":             "The page could not be fetched. Open it in a tab of your own instead.",
		"fetch_timeout":            "The page took too long to fetch. Open it in a tab of your own instead.",
		"unsupported_content":      "That kind of file cannot be read here. Find an HTML page with the same information.",
		"device_execution_failed":  "The step failed on the device. Read the page and try another way to reach the same control.",
		"browser_text_not_kept":    "The editor did not keep the text. Type it with real keystrokes, in parts if it is long.",
		"device_execution_expired": "The step ran out of time on the device. Try a smaller step.",
	}
	hint, ok := hints[code]
	if !ok {
		hint = "Read the page again and try another way."
	}
	return code + ": " + hint
}
