package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// Sub-agents (docs/agents/harness.md, Sub-agents and the notebook). agent_delegate starts
// up to three child runs (with the parent, four views of its chat, docs/agents/browser.md). Each is an ordinary invocation that runs under the
// parent's lease, agent and approval setting, never more; with a browser, the
// parent's device opens a tab per child and signs that child's grant itself.
// Children report into the parent's notebook and return their answers here.

const (
	toolboxAgentsDelegateTasks = "agent.delegate"
	toolboxAgentsWait          = "agent.wait"
	maxDelegatedTasks          = 3
	// A tool call holds this long at most, like a device job; agent_wait
	// picks up children that run longer.
	delegationHold = 270 * time.Second
	spawnTabsOp    = "browser.spawn_tabs"
)

type aiDelegatedChild struct {
	InvocationID string `json:"invocationId"`
	Goal         string `json:"goal"`
	State        string `json:"state"`
	Summary      string `json:"summary,omitempty"`
}

type aiDelegationEvent struct {
	Children []aiDelegatedChild `json:"children"`
}

type delegatedTask struct {
	Goal         string          `json:"goal"`
	Context      string          `json:"context"`
	OutputSchema json.RawMessage `json:"output_schema"`
}

func delegationToolDescriptors() []agenttools.Descriptor {
	task := map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"goal"},
		"properties": map[string]any{
			"goal":          map[string]any{"type": "string", "minLength": 1, "maxLength": 4000, "description": "The finished result this sub-agent should produce, stated fully: it does not see this conversation."},
			"context":       map[string]any{"type": "string", "maxLength": 8000, "description": "Facts, URLs or constraints it needs."},
			"output_schema": map[string]any{"type": "object", "description": "Optional JSON Schema for its answer, for results you will merge."},
		},
	}
	return []agenttools.Descriptor{
		{Name: toolboxAgentsDelegateTasks, Version: 1, Risk: agent.RiskWrite, Layer: agenttools.LayerPublic, Locality: agenttools.LocalityServer, AuditEvent: "agent.delegated",
			Description: "Split independent parts of a large task across up to three sub-agents that work at the same time, each in its own browser view when this run has a browser. Give them out like a queue: when one finishes and more parts remain, call it again. Use it for breadth: researching several companies, products or sources, or filling several separate forms. Each task must stand alone. Sub-agents act with your permissions, write their findings to this task's notebook and return their answers; this call waits for them for up to about four minutes, then returns whatever is still running so you can call agent_wait.",
			InputSchema: MustAPIRawJSON(map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"tasks"},
				"properties": map[string]any{
					"tasks":   map[string]any{"type": "array", "minItems": 1, "maxItems": maxDelegatedTasks, "items": task},
					"browser": map[string]any{"type": "boolean", "description": "Give each sub-agent its own browser tab. Default true when this run has a browser."},
				},
			}), OutputSchema: agentToolObjectOutputSchema()},
		{Name: toolboxAgentsWait, Version: 1, Risk: agent.RiskRead, Idempotent: true, Layer: agenttools.LayerPublic, Locality: agenttools.LocalityServer,
			Description: "Wait up to about four minutes for this task's sub-agents to finish, and return each one's state and answer.",
			InputSchema: MustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}), OutputSchema: agentToolObjectOutputSchema()},
	}
}

func parseDelegatedTasks(raw json.RawMessage) ([]delegatedTask, bool, error) {
	var input struct {
		Tasks   []delegatedTask `json:"tasks"`
		Browser *bool           `json:"browser"`
	}
	if json.Unmarshal(raw, &input) != nil || len(input.Tasks) == 0 || len(input.Tasks) > maxDelegatedTasks {
		return nil, false, agent.ErrInvalidRequest(fmt.Sprintf("give 1–%d tasks", maxDelegatedTasks))
	}
	for index, task := range input.Tasks {
		if strings.TrimSpace(task.Goal) == "" || len(task.Goal) > 4000 || len(task.Context) > 8000 {
			return nil, false, agent.ErrInvalidRequest(fmt.Sprintf("task %d needs a goal of up to 4000 characters", index+1))
		}
		if len(task.OutputSchema) > 0 && string(task.OutputSchema) != "null" {
			var schema map[string]any
			if len(task.OutputSchema) > 8<<10 || json.Unmarshal(task.OutputSchema, &schema) != nil || schema["type"] != "object" {
				return nil, false, agent.ErrInvalidRequest(fmt.Sprintf("task %d's output_schema must be a JSON Schema with type object", index+1))
			}
		} else {
			input.Tasks[index].OutputSchema = nil
		}
	}
	return input.Tasks, input.Browser == nil || *input.Browser, nil
}

// delegatedPrompt is everything a child knows: it never sees the parent's
// conversation.
func delegatedPrompt(task delegatedTask) string {
	var prompt strings.Builder
	prompt.WriteString("You are a sub-agent working on one part of a larger task for the user. Finish this part completely on your own; there is no one to ask.\n\nGoal: ")
	prompt.WriteString(strings.TrimSpace(task.Goal))
	if context := strings.TrimSpace(task.Context); context != "" {
		prompt.WriteString("\n\nContext:\n")
		prompt.WriteString(context)
	}
	prompt.WriteString("\n\nAppend what you find, with source URLs, to the task notebook's findings with log_work_write as you go. Do not rewrite its plan.")
	if len(task.OutputSchema) > 0 {
		prompt.WriteString("\n\nEnd with agent_finish whose summary is only a JSON object matching this schema:\n")
		prompt.Write(task.OutputSchema)
	} else {
		prompt.WriteString("\n\nEnd with agent_finish: a concise answer with the facts and their sources.")
	}
	return prompt.String()
}

func (s *SpacesService) delegationToolRegistrations(record *db.AIInvocationRecord, body aiInvocationInput) []agenttools.Registration {
	// Sub-agents never start sub-agents of their own.
	if record == nil || body.DelegatedFrom != "" {
		return nil
	}
	descriptors := delegationToolDescriptors()
	return []agenttools.Registration{
		{Descriptor: descriptors[0], Handler: func(ctx context.Context, invocation agenttools.Invocation, request agent.ToolRequest) (json.RawMessage, error) {
			return s.delegate(ctx, record, body, request)
		}},
		{Descriptor: descriptors[1], Handler: func(ctx context.Context, invocation agenttools.Invocation, _ agent.ToolRequest) (json.RawMessage, error) {
			children, err := s.database.DelegationChildren(ctx, record.UserID, record.ID, "")
			if err != nil {
				return nil, err
			}
			if len(children) == 0 {
				return nil, agent.ErrInvalidRequest("this task has no sub-agents; start them with agent_delegate")
			}
			return s.awaitChildren(ctx, record, "")
		}},
	}
}

func (s *SpacesService) delegate(ctx context.Context, record *db.AIInvocationRecord, body aiInvocationInput, request agent.ToolRequest) (json.RawMessage, error) {
	tasks, wantBrowser, err := parseDelegatedTasks(request.Arguments)
	if err != nil {
		return nil, err
	}
	callID := strings.TrimSpace(request.ID)
	if callID == "" || len(callID) > 200 {
		return nil, db.ErrSpaceInvalid
	}
	existing, err := s.database.DelegationChildren(ctx, record.UserID, record.ID, callID)
	if err != nil {
		return nil, err
	}
	// A retried call waits for the children it already started.
	if len(existing) == 0 {
		var tabs []aiInvocationDeviceContext
		if wantBrowser {
			labels := make([]string, len(tasks))
			for index, task := range tasks {
				labels[index] = viewLabel(task.Goal)
			}
			if tabs, err = s.spawnChildTabs(ctx, record, body, callID, labels); err != nil {
				return nil, err
			}
		}
		for index, task := range tasks {
			var tab *aiInvocationDeviceContext
			if index < len(tabs) {
				tab = &tabs[index]
			}
			child, err := s.startDelegatedInvocation(ctx, record, body, callID, index, task, tab)
			if err != nil {
				return nil, err
			}
			if err := s.database.InsertDelegationChild(ctx, record.UserID, record.ID, db.DelegationChild{
				CallID: callID, TaskIndex: index, Goal: task.Goal, OutputSchema: task.OutputSchema, ChildInvocationID: child,
			}); err != nil {
				return nil, err
			}
		}
	}
	return s.awaitChildren(ctx, record, callID)
}

// spawnChildTabs asks the parent's device to open one tab per child in the
// parent's browser workspace and to sign each child's grant. Without a browser
// that can do this, children work without one.
// viewLabel names a sub-agent's view after its goal's first few words.
func viewLabel(goal string) string {
	words := strings.Fields(goal)
	if len(words) > 4 {
		words = words[:4]
	}
	label := strings.Join(words, " ")
	if runes := []rune(label); len(runes) > 32 {
		label = string(runes[:32])
	}
	return label
}

func (s *SpacesService) spawnChildTabs(ctx context.Context, record *db.AIInvocationRecord, body aiInvocationInput, callID string, labels []string) ([]aiInvocationDeviceContext, error) {
	count := len(labels)
	contexts, err := s.database.AIInvocationContexts(ctx, record.UserID, record.ID)
	if err != nil {
		return nil, err
	}
	scope, device := "", ""
	for _, item := range contexts {
		var capabilities []string
		if item.Kind == "browser_tab" && item.State == "attached" && json.Unmarshal(item.Capabilities, &capabilities) == nil && containsString(capabilities, spawnTabsOp) {
			scope, device = item.OpaqueRef, item.DeviceID
			break
		}
	}
	if scope == "" {
		return nil, nil
	}
	agentID := body.AgentID
	if agentID == "" {
		agentID = "misty-unified"
	}
	// The conversation finds the parent's own agent window, where the children's tabs open beside its own.
	input := MustAPIRawJSON(map[string]any{"scopeId": scope, "count": count, "labels": labels, "conversationId": record.ConversationID})
	// On another device's session, its own task holds the lease.
	config := MustAPIRawJSON(map[string]any{"agentId": agentID, "taskId": scopeTaskID(body, scope)})
	job, err := s.database.QueueAIInvocationDeviceNodeJob(ctx, record.UserID, record.ID, "delegate_tabs_"+callID, 1, scope, spawnTabsOp, spawnTabsOp, input, config,
		MustAPIRawJSON(map[string]any{"type": "object"}), agentToolObjectOutputSchema())
	if err != nil {
		return nil, err
	}
	current, err := waitForDeviceJob(ctx, s.database, record.UserID, job.ID, job.DeadlineAt)
	if err != nil || current.State != "completed" {
		return nil, agent.ErrInvalidRequest("Misty could not open browser tabs for the sub-agents. Call agent_delegate with browser false to run them without one.")
	}
	var output struct {
		Tabs []aiInvocationDeviceContext `json:"tabs"`
	}
	if json.Unmarshal(current.Output, &output) != nil {
		return nil, agent.ErrInvalidRequest("the device opened the wrong number of tabs for the sub-agents")
	}
	if err := validateDeviceOpenedTabs(output.Tabs, device, count); err != nil {
		return nil, agent.ErrInvalidRequest("Misty could not open browser tabs for the sub-agents. Call agent_delegate with browser false to run them without one.")
	}
	if err := verifyDeviceContextGrants(ctx, s.database, record.UserID, body.AgentID, output.Tabs); err != nil {
		return nil, err
	}
	return output.Tabs, nil
}

func (s *SpacesService) startDelegatedInvocation(ctx context.Context, parent *db.AIInvocationRecord, body aiInvocationInput, callID string, index int, task delegatedTask, tab *aiInvocationDeviceContext) (string, error) {
	model := strings.TrimSpace(body.ModelID)
	if route, err := s.database.AIModelBrowsingRoute(ctx, parent.UserID, parent.ID); err == nil && route.Model != "" && agent.ClientModelChoice() {
		// Children navigate on the account's Browsing model.
		model = route.Model
	}
	child := aiInvocationInput{
		Mode: "drawer", SurfaceID: "global", Trigger: "delegated", Prompt: delegatedPrompt(task), Timezone: body.Timezone,
		AgentID: body.AgentID, SpaceID: body.SpaceID, ExecutionMode: body.ExecutionMode, TaskID: body.TaskID, WindowLabel: body.WindowLabel,
		CollaborationMode: body.CollaborationMode, PermissionMode: body.PermissionMode, ModelID: model, ReasoningEffort: body.ReasoningEffort,
		IdempotencyKey: "delegate:" + parent.ID + ":" + callID + ":" + strconv.Itoa(index),
		NotebookKey:    notebookKey(parent, body), DelegatedFrom: parent.ID,
	}
	if tab != nil {
		child.DeviceContexts = []aiInvocationDeviceContext{*tab}
	}
	payload, err := json.Marshal(child)
	if err != nil {
		return "", err
	}
	stored, created, err := s.database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{
		DispatchRuntime: true, ModelTurnLimit: parent.ModelTurnLimit,
		ID: "invocation_" + uuid.NewString(), UserID: parent.UserID, SurfaceID: "global", Mode: "drawer", Trigger: "message", State: "queued",
		IdempotencyKey: child.IdempotencyKey, RequestPayload: payload, ExpiresAt: time.Now().UTC().Add(aiInvocationTTL),
	})
	if err != nil {
		return "", err
	}
	if created {
		// Freeze before the runtime would freeze the defaults, so the child's
		// agent role runs on the Browsing model.
		planned, forced := planRoutes(ctx, s.database, parent.UserID, defaultAIRoutes(model, body.ReasoningEffort))
		overrides := map[string]string{}
		if model != "" {
			overrides["agent"] = model
		}
		if _, err := s.database.FreezeAIModelRoutes(ctx, parent.UserID, stored.ID, planned, overrides, forced); err != nil {
			return "", err
		}
	}
	if s.aiInvocations != nil {
		_, _ = s.aiInvocations.restoreDurable(ctx, stored)
	}
	return stored.ID, nil
}
