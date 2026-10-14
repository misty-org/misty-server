package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func (s *SpacesService) executeBrowserAgentTool(
	ctx context.Context,
	run *db.SpaceRun,
	tool serveragent.ToolRequest,
) (json.RawMessage, error) {
	return s.executeBrowserAgentToolInvocation(ctx, agenttools.Invocation{
		UserID: run.RequestingMemberID, SpaceID: run.SpaceID, AgentID: run.AgentID, RunID: run.ID,
	}, tool)
}

func (s *SpacesService) executeBrowserAgentToolInvocation(
	ctx context.Context,
	invocation agenttools.Invocation,
	tool serveragent.ToolRequest,
) (json.RawMessage, error) {
	scopeID, err := browserToolScopeID(tool.Arguments)
	if err != nil {
		return nil, err
	}
	if err := authorizeNativeAgentBrowserScope(ctx, s.database, invocation, scopeID); err != nil {
		return nil, err
	}
	schema, err := browserToolSchema(tool.Name)
	if err != nil {
		return nil, err
	}
	configData := s.browserToolConfig(ctx, invocation)
	if isAIInvocationRuntimeID(invocation.RunID) {
		if err := s.applyAIInvocationBrowserConfig(ctx, invocation, tool, scopeID, configData); err != nil {
			return nil, err
		}
	} else if tool.Name == "browser.upload" {
		return nil, db.ErrSpaceForbidden
	}
	if err := validateBrowserToolArguments(tool); err != nil {
		return nil, err
	}
	if browserPageActTools[tool.Name] {
		if waiting, err := s.approveBrowserPageSteps(ctx, invocation, tool, scopeID); err != nil || waiting != nil {
			return waiting, err
		}
	}
	if tool.Name == screenActTool {
		if waiting, err := s.approveScreenAct(ctx, invocation, tool, scopeID); err != nil || waiting != nil {
			return waiting, err
		}
	}
	job, err := s.queueBrowserDeviceJob(ctx, invocation, tool, scopeID, schema, configData)
	if err != nil {
		return nil, err
	}
	return s.awaitBrowserDeviceJob(ctx, invocation, tool, job)
}

var errBrowserWebviewUnavailable = errors.New("browser_webview_unavailable")
var errDesktopAccessibilityRequired = errors.New("desktop_accessibility_required")
var errDesktopScreenRecordingRequired = errors.New("desktop_screen_recording_required")

var (
	errBrowserSnapshotStale   = errors.New("browser_snapshot_stale")
	errWorkspaceSnapshotStale = errors.New("workspace_snapshot_stale")
)

var errBrowserObservationFailed = errors.New("browser_observation_failed")

// Observations have no external effect, so an interrupted one is a retryable
// failure, never an uncertain effect that would end the run.
func browserObservation(operation string) bool {
	switch operation {
	case "browser.inspect", "browser.visual", "browser.downloads.list", "browser.workspace.visual":
		return true
	}
	return browserPageReadTools[operation]
}

// validBrowserUploadTarget requires exactly one file input: a snapshot ref or
// an inspection elementRef.
func validBrowserUploadTarget(raw json.RawMessage) bool {
	var target struct {
		ElementRef string `json:"elementRef"`
		Ref        string `json:"ref"`
	}
	if json.Unmarshal(raw, &target) != nil {
		return false
	}
	return (target.ElementRef != "") != (target.Ref != "")
}

func browserObservationFailure(code string) error {
	return errors.Join(errBrowserObservationFailed, errors.New(strings.TrimSpace(code)))
}

func browserDeviceFailureForOperation(code, operation string) error {
	if code == "browser_snapshot_stale" && strings.HasPrefix(operation, "browser.workspace.") {
		return errors.Join(db.ErrAgentToolboxNotAttempted, errBrowserSnapshotStale, errWorkspaceSnapshotStale)
	}
	return browserDeviceFailure(code)
}

func browserDeviceFailure(code string) error {
	if code == "desktop_accessibility_required" {
		return errDesktopAccessibilityRequired
	}
	if code == "desktop_screen_recording_required" {
		return errDesktopScreenRecordingRequired
	}
	if code == "browser_webview_unavailable" {
		return errBrowserWebviewUnavailable
	}
	if code == "browser_snapshot_stale" {
		// Native code emits this only before dispatch. Preserve that evidence
		// through the write journal so it does not become an uncertain effect.
		return errors.Join(db.ErrAgentToolboxNotAttempted, errBrowserSnapshotStale)
	}
	if code == "device_unavailable" || code == "browser_tab_closed" {
		return workflowv2.ErrDeviceUnavailable
	}
	return errors.New("browser device tool failed: " + code)
}

type browserUploadSource struct {
	AttachmentID  string `json:"attachmentId,omitempty"`
	DownloadID    string `json:"downloadId,omitempty"`
	SourceScopeID string `json:"sourceScopeId,omitempty"`
}

func parseBrowserUploadSource(raw json.RawMessage) (browserUploadSource, error) {
	var source browserUploadSource
	if json.Unmarshal(raw, &source) != nil {
		return source, db.ErrSpaceInvalid
	}
	attachment := source.AttachmentID != "" && len(source.AttachmentID) <= 200 && source.DownloadID == "" && source.SourceScopeID == ""
	download := source.AttachmentID == "" && source.DownloadID != "" && len(source.DownloadID) <= 200 && len(source.SourceScopeID) >= 8 && len(source.SourceScopeID) <= 256
	if !attachment && !download {
		return source, db.ErrSpaceInvalid
	}
	return source, nil
}

func (s *SpacesService) stopBrowserDeviceTool(userID, jobID, operation string) (json.RawMessage, error) {
	job, err := s.database.StopWorkflowDeviceNodeJob(userID, jobID)
	if err != nil {
		if browserObservation(operation) {
			return nil, errors.Join(errBrowserObservationFailed, err)
		}
		return nil, errors.Join(db.ErrAgentToolboxActionUnknown, err)
	}
	switch job.State {
	case "completed":
		if operation == "browser.extract" {
			// The page was read but the run stopped before extraction.
			return nil, browserObservationFailure("browser_extract_interrupted")
		}
		return job.Output, nil
	case "canceled":
		return nil, errors.Join(db.ErrAgentToolboxNotAttempted, workflowv2.ErrDeviceUnavailable)
	case "failed":
		return nil, browserDeviceFailureForOperation(job.ErrorCode, job.Operation)
	default:
		if browserObservation(operation) {
			return nil, browserObservationFailure(job.ErrorCode)
		}
		return nil, db.ErrAgentToolboxActionUnknown
	}
}

// browserConfirmKinds are the page actions the device pauses on until the
// person allows them in the island. Agents act on their own unless the account
// asks first; paying has its own switch.
func browserConfirmKinds(settings db.AIUserSettings) []string {
	if settings.AppActionsAsk {
		return []string{"payment", "send", "delete", "publish"}
	}
	if settings.PaymentsAsk {
		return []string{"payment"}
	}
	return []string{}
}
