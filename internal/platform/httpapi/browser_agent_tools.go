package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/browseractions"
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
	var input struct {
		ScopeID string `json:"scopeId"`
	}
	if json.Unmarshal(tool.Arguments, &input) != nil || len(input.ScopeID) < 8 {
		return nil, db.ErrSpaceInvalid
	}
	if err := authorizeNativeAgentBrowserScope(ctx, s.database, invocation, input.ScopeID); err != nil {
		return nil, err
	}
	var schema json.RawMessage
	for _, descriptor := range browserToolDescriptors() {
		if descriptor.Name == tool.Name {
			schema = descriptor.InputSchema
			break
		}
	}
	if len(schema) == 0 {
		return nil, workflowv2.ErrCapabilityDenied
	}
	agentID := invocation.AgentID
	if agentID == "" {
		agentID = "misty-unified"
	}
	configData := map[string]any{"agentId": agentID}
	if isAIInvocationRuntimeID(invocation.RunID) {
		record, lookupErr := s.database.AIInvocationByID(ctx, invocation.UserID, invocation.RunID)
		if lookupErr != nil {
			return nil, lookupErr
		}
		var body aiInvocationInput
		_ = json.Unmarshal(record.RequestPayload, &body)
		if strings.HasPrefix(tool.Name, "browser.workspace.") {
			if body.ExecutionMode != "agent" || body.WindowLabel != "main" || body.TaskID == "" {
				return nil, db.ErrSpaceForbidden
			}
			matched := false
			for _, target := range body.DeviceContexts {
				var metadata map[string]any
				_ = json.Unmarshal(target.Metadata, &metadata)
				if target.OpaqueRef == input.ScopeID && metadata["workspace_control"] == true {
					matched = true
				}
			}
			if !matched {
				return nil, db.ErrSpaceForbidden
			}
		}
		configData["taskId"] = body.TaskID
		if body.AgentID != "" {
			configData["agentId"] = body.AgentID
		}
		if tool.Name == "browser.upload" {
			upload, err := parseBrowserUploadSource(tool.Arguments)
			if err != nil {
				return nil, err
			}
			if upload.DownloadID != "" {
				// Download reuse is restricted to a live personal-agent task. The
				// native boundary also verifies task ownership and pinned bytes.
				if body.AgentID == "" || body.TaskID == "" {
					return nil, db.ErrSpaceForbidden
				}
				if err := authorizeNativeAgentBrowserScope(ctx, s.database, invocation, upload.SourceScopeID); err != nil {
					return nil, err
				}
				configData["downloadUpload"] = upload
			} else {
				file, lookupErr := s.database.AIConversationAttachment(ctx, invocation.UserID, upload.AttachmentID)
				if lookupErr != nil || file.LifecycleState != "ready" || file.ConversationID != record.ConversationID || file.InvocationID == "" {
					return nil, db.ErrSpaceForbidden
				}
				configData["upload"] = map[string]any{"id": file.ID, "name": file.DisplayName, "mimeType": file.MIMEType, "byteSize": file.ByteSize, "sha256": file.SHA256}
			}
		}
	} else if tool.Name == "browser.upload" {
		return nil, db.ErrSpaceForbidden
	}
	config := TestingMustAPIRawJSON(configData)
	var job *db.WorkflowDeviceNodeJob
	var err error
	if isAIInvocationRuntimeID(invocation.RunID) {
		job, err = s.database.QueueAIInvocationDeviceNodeJob(
			ctx, invocation.UserID, invocation.RunID, "browser_tool_"+tool.ID, 1,
			input.ScopeID, tool.Name, tool.Name, tool.Arguments, config,
			schema, agentToolObjectOutputSchema(),
		)
	} else {
		job, err = s.database.QueueWorkflowDeviceNodeJob(
			ctx, invocation.UserID, invocation.RunID, "browser_tool_"+tool.ID, 1,
			input.ScopeID, tool.Name, tool.Name, tool.Arguments, config,
			schema, agentToolObjectOutputSchema(),
		)
	}
	if errors.Is(err, db.ErrDeviceNotFound) {
		return nil, errors.Join(db.ErrAgentToolboxNotAttempted, workflowv2.ErrDeviceUnavailable)
	}
	if err != nil {
		return nil, err
	}
	if job.State == "canceled" && job.ControlVersion == 2 && job.ExecutionStartedAt == nil {
		job, err = s.database.RearmUnstartedBrowserJob(ctx, invocation.UserID, job)
		if err != nil {
			return nil, errors.Join(db.ErrAgentToolboxNotAttempted, err)
		}
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(time.Until(job.DeadlineAt))
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			return s.stopBrowserDeviceTool(invocation.UserID, job.ID)
		case <-timeout.C:
			return s.stopBrowserDeviceTool(invocation.UserID, job.ID)
		case <-ticker.C:
			current, lookupErr := s.database.WorkflowDeviceNodeJob(ctx, invocation.UserID, job.ID)
			if lookupErr != nil {
				return s.stopBrowserDeviceTool(invocation.UserID, job.ID)
			}
			switch current.State {
			case "completed":
				return current.Output, nil
			case "uncertain":
				return nil, db.ErrAgentToolboxActionUnknown
			case "canceled":
				return nil, errors.Join(db.ErrAgentToolboxNotAttempted, workflowv2.ErrDeviceUnavailable)
			case "failed":
				return nil, browserDeviceFailure(current.ErrorCode)
			}
		}
	}
}

func browserDeviceFailure(code string) error {
	if code == "browser_snapshot_stale" {
		// Native code emits this only before dispatch. Preserve that evidence
		// through the write journal so it does not become an uncertain effect.
		return errors.Join(db.ErrAgentToolboxNotAttempted, browseractions.ErrStale)
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

func (s *SpacesService) stopBrowserDeviceTool(userID, jobID string) (json.RawMessage, error) {
	job, err := s.database.StopWorkflowDeviceNodeJob(userID, jobID)
	if err != nil {
		return nil, errors.Join(db.ErrAgentToolboxActionUnknown, err)
	}
	switch job.State {
	case "completed":
		return job.Output, nil
	case "canceled":
		return nil, errors.Join(db.ErrAgentToolboxNotAttempted, workflowv2.ErrDeviceUnavailable)
	case "failed":
		return nil, browserDeviceFailure(job.ErrorCode)
	default:
		return nil, db.ErrAgentToolboxActionUnknown
	}
}
