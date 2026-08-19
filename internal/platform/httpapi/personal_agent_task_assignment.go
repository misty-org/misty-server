package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

type explicitTaskSourceRef struct {
	Kind        string `json:"kind"`
	ResourceID  string `json:"resource_id"`
	DisplayName string `json:"display_name,omitempty"`
	Version     any    `json:"version,omitempty"`
}

func (s *SpacesService) queueAssignedPersonalAgent(ctx context.Context, userID string, task *db.SpaceTask) {
	if task == nil || task.AssigneeAgentID == "" {
		return
	}
	_, _, _ = s.database.ClaimAssignedAgentTaskRun(ctx, userID, *task)
}

func (s *SpacesService) resolveAssignedTaskToolbox(ctx context.Context, run *db.SpaceRun) (*agenttools.Registry, agenttools.Invocation, serveragent.ToolManifest, error) {
	registrations := []agenttools.Registration{
		agenttools.Registration{Descriptor: assignedTasksQueryToolDescriptor(), Handler: func(toolCtx context.Context, _ agenttools.Invocation, tool serveragent.ToolRequest) (json.RawMessage, error) {
			return s.executeAssignedTaskQuery(toolCtx, run, tool)
		}},
		agenttools.Registration{Descriptor: assignedTasksUpdateToolDescriptor(), Handler: func(toolCtx context.Context, _ agenttools.Invocation, tool serveragent.ToolRequest) (json.RawMessage, error) {
			return s.executeAssignedTaskUpdate(toolCtx, run, tool)
		}},
		agenttools.Registration{Descriptor: assignedTaskActivityToolDescriptor(), Handler: func(toolCtx context.Context, _ agenttools.Invocation, tool serveragent.ToolRequest) (json.RawMessage, error) {
			return s.executeAssignedTaskActivity(toolCtx, run, tool)
		}},
		agenttools.Registration{Descriptor: assignedTaskAttachedFilesToolDescriptor(), Handler: func(toolCtx context.Context, _ agenttools.Invocation, tool serveragent.ToolRequest) (json.RawMessage, error) {
			return s.executeAssignedTaskAttachedFiles(toolCtx, run, tool)
		}},
	}
	requested := []string{toolboxTasksQuery, "tasks.update_assigned", "task.activity.write", "attached_files.read"}
	if contexts, contextErr := s.database.AgentRunDeviceGrants(ctx, run.OwnerUserID, run.ID); contextErr == nil {
		for _, descriptor := range browserToolDescriptors() {
			if !activeBrowserCapability(contexts, descriptor.Name) {
				continue
			}
			registrations = append(registrations, agenttools.Registration{Descriptor: descriptor, Handler: func(toolCtx context.Context, _ agenttools.Invocation, tool serveragent.ToolRequest) (json.RawMessage, error) {
				return s.executeBrowserAgentTool(toolCtx, run, tool)
			}})
			requested = append(requested, descriptor.Name)
		}
	}
	mcpHandler := func(toolCtx context.Context, _ agenttools.Invocation, tool serveragent.ToolRequest) (json.RawMessage, error) {
		return s.executeMCPAgentTool(toolCtx, run, tool, false, "task_assignment")
	}
	registrations, requested = s.appendPersonalAgentMCPTools(ctx, run.RequestingMemberID, run.AgentID, registrations, requested, mcpHandler)
	toolbox, err := agenttools.New(registrations...)
	if err != nil {
		return nil, agenttools.Invocation{}, serveragent.ToolManifest{}, err
	}
	invocation := agenttools.Invocation{
		UserID: run.RequestingMemberID, SpaceID: run.SpaceID, AgentID: run.AgentID, RunID: run.ID,
		Source: "task_assignment", Trigger: "task_assignment", DelegatedApproval: true,
	}
	manifest, err := toolbox.Resolve(ctx, invocation, requested, authorizePersonalAgentTaskTool(s.database))
	return toolbox, invocation, manifest, err
}

func assignedTaskAttachedFilesToolDescriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: "attached_files.read", Version: 1, Description: "Read only files explicitly attached to the assigned Task.",
		Risk: serveragent.RiskRead, InputSchema: TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{}}), OutputSchema: agentToolObjectOutputSchema(),
		AgentPermission: "attached_files.read", AllowCustomAgent: true, Approval: agenttools.ApprovalNone,
		Locality: agenttools.LocalityServer, Idempotent: true, Sources: []string{"task_assignment"},
	}
}

func assignedTasksQueryToolDescriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: toolboxTasksQuery, Version: 1, Description: "Query Tasks visible in the assigned Task's Space.",
		Risk: serveragent.RiskRead, InputSchema: taskAgentToolSchema(false), OutputSchema: agentToolObjectOutputSchema(), RequiredPermission: db.PermissionTasksView,
		AgentPermission: db.PermissionTasksView, AllowCustomAgent: true, Approval: agenttools.ApprovalNone,
		Locality: agenttools.LocalityServer, Idempotent: true, Sources: []string{"task_assignment"},
	}
}

func assignedTasksUpdateToolDescriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: "tasks.update_assigned", Version: 1, Description: "Update only the Task assigned to this Agent run.",
		Risk: serveragent.RiskWrite, InputSchema: TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{"status": map[string]any{"type": "string", "enum": []string{"in_progress", "done", "canceled"}}, "notes": map[string]any{"type": "string"}}}), OutputSchema: agentToolObjectOutputSchema(),
		RequiredPermission: db.PermissionTasksManage, AgentPermission: db.PermissionTasksManage, AllowCustomAgent: true,
		Approval: agenttools.ApprovalNone, Locality: agenttools.LocalityServer, Idempotent: true, AuditEvent: "task.updated", Sources: []string{"task_assignment"},
	}
}

func assignedTaskActivityToolDescriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: "task.activity.write", Version: 1, Description: "Add progress or a result to the assigned Task's activity log.",
		Risk: serveragent.RiskWrite, InputSchema: TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"progress", "result"}}, "message": map[string]any{"type": "string"}}, "required": []string{"kind", "message"}}), OutputSchema: agentToolObjectOutputSchema(),
		RequiredPermission: db.PermissionTasksManage, AgentPermission: db.PermissionTasksManage, AllowCustomAgent: true,
		Approval: agenttools.ApprovalNone, Locality: agenttools.LocalityServer, AuditEvent: "task.activity.created", Sources: []string{"task_assignment"},
	}
}

func authorizePersonalAgentTaskTool(database *db.Database) agenttools.Authorizer {
	return func(ctx context.Context, invocation agenttools.Invocation, descriptor agenttools.Descriptor) (bool, error) {
		if strings.HasPrefix(descriptor.Name, "mcp.") {
			return authorizeMCPAgentTool(ctx, database, invocation, descriptor)
		}
		policy, err := database.EffectivePersonalAgentToolPermissions(ctx, invocation.UserID, invocation.SpaceID, invocation.AgentID)
		if err != nil || !personalAgentToolPolicyAllows(policy, descriptor) {
			return false, err
		}
		if descriptor.RequiredPermission != "" {
			allowed, permissionErr := database.HasSpacePermission(ctx, invocation.UserID, invocation.SpaceID, descriptor.RequiredPermission)
			if permissionErr != nil || !allowed {
				return allowed, permissionErr
			}
		}
		if descriptor.AgentPermission != "" {
			return database.EffectiveAgentSpacePermission(ctx, invocation.UserID, invocation.SpaceID, invocation.AgentID, descriptor.AgentPermission)
		}
		return true, nil
	}
}

func (s *SpacesService) finishPersonalAgentTaskRun(ctx context.Context, run *db.SpaceRun, task *db.SpaceTask, text string, runErr error) {
	if run == nil || task == nil {
		return
	}
	message := strings.TrimSpace(text)
	if message == "" && runErr != nil {
		message = runErr.Error()
	}
	if message == "" {
		message = "Agent run failed"
	}
	result := TestingMustAPIRawJSON(map[string]any{"message": message})
	_, _ = s.database.AddSpaceTaskAgentActivity(ctx, task.ID, task.AssigneeAgentID, run.ID, "failure", message, result)
	_, _ = s.database.FinishSpaceRun(ctx, run.ID, "failed", result, "agent_task_failed")
}

func (s *SpacesService) executeAssignedTaskQuery(ctx context.Context, run *db.SpaceRun, tool serveragent.ToolRequest) (json.RawMessage, error) {
	if err := s.database.ValidatePersonalAgentTaskRun(ctx, run.RequestingMemberID, run.ID, run.SourceTaskID, run.AgentID); err != nil {
		return nil, err
	}
	var input struct {
		Query  string `json:"query"`
		Status string `json:"status"`
	}
	if json.Unmarshal(tool.Arguments, &input) != nil {
		return nil, db.ErrSpaceInvalid
	}
	page, err := s.database.SpaceTaskPage(ctx, run.RequestingMemberID, run.SpaceID, db.SpaceTaskQuery{Search: input.Query, Status: input.Status, Limit: 50})
	if err != nil {
		return nil, err
	}
	return TestingMustAPIRawJSON(page), nil
}

func (s *SpacesService) executeAssignedTaskUpdate(ctx context.Context, run *db.SpaceRun, tool serveragent.ToolRequest) (json.RawMessage, error) {
	if err := s.database.ValidatePersonalAgentTaskRun(ctx, run.RequestingMemberID, run.ID, run.SourceTaskID, run.AgentID); err != nil {
		return nil, err
	}
	var input struct {
		Status string  `json:"status"`
		Notes  *string `json:"notes"`
	}
	if json.Unmarshal(tool.Arguments, &input) != nil {
		return nil, db.ErrSpaceInvalid
	}
	current, err := s.database.SpaceTaskForMember(ctx, run.RequestingMemberID, run.SpaceID, run.SourceTaskID)
	if err != nil || current.AssigneeAgentID != run.AgentID {
		if err != nil {
			return nil, err
		}
		return nil, db.ErrSpaceForbidden
	}
	if input.Status != "" {
		current.Status = input.Status
	}
	if input.Notes != nil {
		current.Notes = *input.Notes
	}
	updated, err := s.database.UpdateSpaceTask(ctx, run.RequestingMemberID, *current)
	if err != nil {
		return nil, err
	}
	kind := "status"
	if updated.Status == "done" {
		kind = "completed"
	}
	_, _ = s.database.AddSpaceTaskAgentActivity(ctx, updated.ID, run.AgentID, run.ID, kind, "Updated task status to "+updated.Status, TestingMustAPIRawJSON(map[string]any{"status": updated.Status}))
	return TestingMustAPIRawJSON(updated), nil
}

func (s *SpacesService) executeAssignedTaskActivity(ctx context.Context, run *db.SpaceRun, tool serveragent.ToolRequest) (json.RawMessage, error) {
	if err := s.database.ValidatePersonalAgentTaskRun(ctx, run.RequestingMemberID, run.ID, run.SourceTaskID, run.AgentID); err != nil {
		return nil, err
	}
	var input struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	}
	if json.Unmarshal(tool.Arguments, &input) != nil || input.Kind != "progress" && input.Kind != "result" || strings.TrimSpace(input.Message) == "" {
		return nil, db.ErrSpaceInvalid
	}
	item, err := s.database.AddSpaceTaskAgentActivity(ctx, run.SourceTaskID, run.AgentID, run.ID, input.Kind, input.Message, json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	return TestingMustAPIRawJSON(item), nil
}

func (s *SpacesService) executeAssignedTaskAttachedFiles(ctx context.Context, run *db.SpaceRun, _ serveragent.ToolRequest) (json.RawMessage, error) {
	if err := s.database.ValidatePersonalAgentTaskRun(ctx, run.RequestingMemberID, run.ID, run.SourceTaskID, run.AgentID); err != nil {
		return nil, err
	}
	task, err := s.database.SpaceTaskForMember(ctx, run.RequestingMemberID, run.SpaceID, run.SourceTaskID)
	if err != nil || task.AssigneeAgentID != run.AgentID {
		if err != nil {
			return nil, err
		}
		return nil, db.ErrSpaceForbidden
	}
	content, warnings, sources := s.explicitTaskFileContext(ctx, run.RequestingMemberID, task)
	return TestingMustAPIRawJSON(map[string]any{"content": content, "warnings": warnings, "sources": sources}), nil
}

func (s *SpacesService) explicitTaskFileContext(ctx context.Context, userID string, task *db.SpaceTask) (string, string, []workflowv2.ContentRef) {
	if s.library == nil {
		return "", "Attached files were not read because attached-file access is unavailable.", nil
	}
	var refs []explicitTaskSourceRef
	if json.Unmarshal(task.SourceRefs, &refs) != nil {
		return "", "Attached files could not be read because their references are invalid.", nil
	}
	var content, warnings strings.Builder
	sources := []workflowv2.ContentRef{}
	remaining := 20_000
	for _, ref := range refs {
		if remaining <= 0 {
			warnings.WriteString("\n- Additional attached content was truncated.")
			break
		}
		data, download, err := s.library.ReadExplicitAgentAttachment(ctx, userID, task.SpaceID, ref.Kind, ref.ResourceID, 25_000_000)
		if err != nil {
			name := ref.DisplayName
			if download != nil && download.Filename != "" {
				name = download.Filename
			}
			if name == "" {
				name = ref.ResourceID
			}
			warnings.WriteString("\n- " + name + ": " + explicitAttachmentError(err))
			continue
		}
		name := download.Filename
		text := string(data)
		if len(text) > remaining {
			text = text[:remaining]
			warnings.WriteString("\n- " + name + ": content was truncated to the Agent context limit.")
		}
		remaining -= len(text)
		content.WriteString("\n\n[Explicitly attached file: " + name + "]\n" + text)
		sources = append(sources, workflowv2.ContentRef{SourceKind: ref.Kind, ProviderID: "misty", ResourceID: ref.ResourceID, Version: explicitSourceVersion(ref.Version), Fingerprint: download.SHA256, DisplayName: name, MIMEType: download.MIMEType, PermissionScope: "task:" + task.ID})
	}
	return content.String(), warnings.String(), sources
}

func explicitSourceVersion(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func explicitAttachmentError(err error) string {
	if errors.Is(err, workflowv2.ErrUnsupportedContent) {
		return "unsupported file type; images, audio, and video are not read in this beta"
	}
	if errors.Is(err, db.ErrLibraryForbidden) || errors.Is(err, db.ErrSpaceForbidden) {
		return "access was denied"
	}
	if errors.Is(err, db.ErrLibraryNotFound) {
		return "file is missing or no longer available"
	}
	return "file could not be read"
}
