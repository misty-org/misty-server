package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// Coding is delegated to the person's own coding tools on the computer that
// holds the project. code.delegate starts a child run on that device; the
// device picks the tool, runs it in the project and reports back to the child
// run, whose checklist nests under this run's. code.wait waits for it.
const (
	codeDelegateTool    = "code.delegate"
	codeWaitTool        = "code.wait"
	codingExecutionMode = "coding"
)

// codingToolIDs are the coding tools a chat can choose, as the device names them.
var codingToolIDs = map[string]bool{
	"claude": true, "codex": true, "minimax": true, "kimi": true, "omp": true, "opencode": true,
	"pi": true, "agy": true, "qoder": true, "qoder-cn": true, "grok": true, "dsh": true,
}

// codingToolChoice keeps a known tool the person picked; anything else falls
// back to the Settings default on the device.
func codingToolChoice(id string) string {
	if codingToolIDs[id] {
		return id
	}
	return ""
}

func codeDelegationDescriptors() []agenttools.Descriptor {
	return []agenttools.Descriptor{
		{Name: codeDelegateTool, Version: 1, Risk: agent.RiskWrite, Layer: agenttools.LayerPrivate, Locality: agenttools.LocalityServer, AuditEvent: "coding.delegated",
			Description: "Hand a coding task in an attached project to the person's own coding tool (such as Claude Code or Codex) on the computer that holds the project. State the task fully: the tool does not see this conversation. It works with the chat's Ask or Auto and asks the person before changes that need them. In Plan it only researches and proposes. Waits about four minutes, then returns what is still running so you can call code_wait. Mark the step delegated in your checklist.",
			InputSchema: MustAPIRawJSON(map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"task"},
				"properties": map[string]any{
					"project": map[string]any{"type": "string", "maxLength": 512, "description": "The attached project's name or id; optional when one project is attached."},
					"task":    map[string]any{"type": "string", "minLength": 1, "maxLength": 8000},
					"mode":    map[string]any{"type": "string", "enum": []string{"edit", "plan"}, "description": "plan researches read-only and proposes; edit (default) does the work."},
				},
			}), OutputSchema: agentToolObjectOutputSchema()},
		{Name: codeWaitTool, Version: 1, Risk: agent.RiskRead, Idempotent: true, Layer: agenttools.LayerPrivate, Locality: agenttools.LocalityServer,
			Description: "Wait up to about four minutes for delegated coding to finish, and return each run's state and answer.",
			InputSchema: MustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}), OutputSchema: agentToolObjectOutputSchema()},
	}
}

// codeDelegationToolRegistrations offers coding when a project is attached and
// the account's plan includes coding. Delegated runs never delegate again.
func (s *SpacesService) codeDelegationToolRegistrations(ctx context.Context, record *db.AIInvocationRecord, body aiInvocationInput, projects []deviceGrant, state collaborationState) []agenttools.Registration {
	if record == nil || body.DelegatedFrom != "" || len(projects) == 0 || codingIncluded(ctx, s.database, record.UserID) != nil {
		return nil
	}
	descriptors := codeDelegationDescriptors()
	return []agenttools.Registration{
		{Descriptor: descriptors[0], Handler: func(ctx context.Context, _ agenttools.Invocation, request agent.ToolRequest) (json.RawMessage, error) {
			return s.delegateCoding(ctx, record, body, projects, state, request)
		}},
		{Descriptor: descriptors[1], Handler: func(ctx context.Context, _ agenttools.Invocation, _ agent.ToolRequest) (json.RawMessage, error) {
			return s.awaitChildren(ctx, record, "")
		}},
	}
}

func pickProject(projects []deviceGrant, name string) (deviceGrant, error) {
	name = strings.TrimSpace(name)
	for _, project := range projects {
		if name == "" && len(projects) == 1 || project.scopeID == name || strings.EqualFold(project.name, name) {
			return project, nil
		}
	}
	names := []string{}
	for _, project := range projects {
		names = append(names, project.name)
	}
	return deviceGrant{}, agent.ErrInvalidRequest("name one of the attached projects: " + strings.Join(names, ", "))
}

func (s *SpacesService) delegateCoding(ctx context.Context, parent *db.AIInvocationRecord, body aiInvocationInput, projects []deviceGrant, state collaborationState, request agent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		Project string `json:"project"`
		Task    string `json:"task"`
		Mode    string `json:"mode"`
	}
	if json.Unmarshal(request.Arguments, &input) != nil || strings.TrimSpace(input.Task) == "" {
		return nil, agenttools.ErrArgumentsInvalid
	}
	// The catalog leaves coding out for plans without it; a call that names
	// the tool anyway is refused here.
	if err := codingIncluded(ctx, s.database, parent.UserID); err != nil {
		return MustAPIRawJSON(map[string]any{
			"status": codePlanUpgradeRequired, "code": codePlanUpgradeRequired,
			"message":      "This account's plan doesn't include coding, so the coding didn't start. Tell the person coding is part of Misty Pro and above, and don't try again.",
			"user_message": planUpgradeMessage,
		}), nil
	}
	project, err := pickProject(projects, input.Project)
	if err != nil {
		return nil, err
	}
	if existing, err := s.database.DelegationChildren(ctx, parent.UserID, parent.ID, request.ID); err == nil && len(existing) > 0 {
		return s.awaitChildren(ctx, parent, request.ID)
	}
	// Delegated coding inherits Plan: a planning run gets a read-only launch.
	mode := "edit"
	if state.planning() || input.Mode == "plan" {
		mode = "plan"
	}
	childID, err := s.startCodingChild(ctx, parent, body, request.ID, input.Task, mode, project.scopeID)
	if err != nil {
		return nil, err
	}
	if err := s.database.InsertDelegationChild(ctx, parent.UserID, parent.ID, db.DelegationChild{CallID: request.ID, Goal: input.Task, ChildInvocationID: childID}); err != nil {
		return nil, err
	}
	// The agent and conversation let the device save a snapshot of the
	// project to the agent's Space once the run finishes.
	job, _ := json.Marshal(map[string]any{"childInvocationId": childID, "task": input.Task, "mode": mode, "permissionMode": body.PermissionMode, "tool": codingToolChoice(body.CodingTool),
		"project": project.name, "agentId": body.AgentID, "conversationId": parent.ConversationID})
	config, _ := json.Marshal(map[string]any{"agentId": firstAIText(body.AgentID, "misty-unified")})
	schema := json.RawMessage(`{"type":"object"}`)
	if _, err := s.database.QueueAIInvocationDeviceNodeJob(ctx, parent.UserID, parent.ID, "coding_"+request.ID, 0, project.scopeID, codeDelegateTool, codeDelegateTool, job, config, schema, schema); err != nil {
		reason := "couldn't be reached"
		if errors.Is(err, db.ErrDeviceNotFound) {
			reason = "is offline"
		}
		device := firstAIText(project.deviceName, "The computer with this project")
		_ = s.aiInvocations.fail(childID, device+" "+reason+".")
		return MustAPIRawJSON(map[string]any{
			"status": "device_offline", "device": device,
			"message":      device + " " + reason + ", so the coding couldn't start. Ask the person to open Misty on it, then call code_delegate again.",
			"user_message": "Waiting for " + device + ". Open Misty on it to continue.",
		}), nil
	}
	return s.awaitChildren(ctx, parent, request.ID)
}

// startCodingChild creates the delegated coding run. It never runs on the
// cloud runtime: the device reports its events, usage and answer.
func (s *SpacesService) startCodingChild(ctx context.Context, parent *db.AIInvocationRecord, body aiInvocationInput, callID, task, mode, project string) (string, error) {
	collaboration := db.ConversationModeAct
	if mode == "plan" {
		collaboration = db.ConversationModePlan
	}
	child := aiInvocationInput{
		Mode: "drawer", SurfaceID: "global", Trigger: "delegated", Prompt: task, Timezone: body.Timezone, AgentID: body.AgentID, SpaceID: body.SpaceID,
		ExecutionMode: codingExecutionMode, CollaborationMode: collaboration, PermissionMode: body.PermissionMode,
		IdempotencyKey: "code:" + parent.ID + ":" + callID, DelegatedFrom: parent.ID, CodingProject: project,
	}
	payload, err := json.Marshal(child)
	if err != nil {
		return "", err
	}
	stored, created, err := s.database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{
		ID: "invocation_" + uuid.NewString(), UserID: parent.UserID, SurfaceID: "global", Mode: "drawer", Trigger: "message", State: "queued",
		IdempotencyKey: child.IdempotencyKey, RequestPayload: payload, ExpiresAt: time.Now().UTC().Add(aiInvocationTTL),
	})
	if err != nil {
		return "", err
	}
	if _, err := s.aiInvocations.restoreDurable(ctx, stored); err != nil {
		return "", err
	}
	if created {
		if err := s.aiInvocations.append(stored.ID, aiInvocationEvent{Type: "invocation.started"}); err != nil {
			return "", err
		}
	}
	return stored.ID, nil
}

// attachedProjects are the project folders attached to a run, with the name of
// the device that holds each.
func (s *SpacesService) attachedProjects(ctx context.Context, record *db.AIInvocationRecord) []deviceGrant {
	if record == nil || s.database == nil {
		return nil
	}
	contexts, err := s.database.AIInvocationContexts(ctx, record.UserID, record.ID)
	if err != nil {
		return nil
	}
	names := map[string]string{}
	if devices, err := s.database.AccountDevices(ctx, record.UserID); err == nil {
		for _, device := range devices {
			names[device.ID] = device.Name
		}
	}
	projects := []deviceGrant{}
	for _, item := range contexts {
		if item.Kind == "project_folder" {
			projects = append(projects, deviceGrant{scopeID: item.OpaqueRef, name: item.DisplayName, deviceID: item.DeviceID, deviceName: names[item.DeviceID]})
		}
	}
	return projects
}
