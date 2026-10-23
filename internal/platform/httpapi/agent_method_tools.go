package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// Saved workflows (memory.workflow, docs/agents/harness.md, Workflows as memory) are owned
// by one agent and stored as methods. The agent reads a workflow's rendered
// instructions and follows them in its current run, so a workflow never starts a
// second run or grants a tool. Each has two owners: the person's request, which
// changes only when they ask, and the steps the agent learned from running it,
// kept as versions (agent_method_lessons). Workflows live in a Space's memory
// tree; a run sees and saves only its own Space's workflows.
func methodToolRegistrations(database *db.Database, search memorySearchFunc) []agenttools.Registration {
	base := agenttools.Descriptor{Version: 1, OutputSchema: agentToolObjectOutputSchema(), Layer: agenttools.LayerPrivate, Locality: agenttools.LocalityServer,
		Risk: agent.RiskRead, Idempotent: true}
	list, use, find := base, base, base
	list.Name = "memory.workflow.list"
	list.Description = "List the saved workflows this agent has, with the inputs each one takes."
	list.InputSchema = agentToolSchema(map[string]any{}, nil)
	find.Name = "memory.workflow.find"
	find.Description = "Find saved workflows that match a request, best first. Use it when a request looks like something done before."
	find.InputSchema = agentToolSchema(map[string]any{
		"request": map[string]any{"type": "string", "minLength": 1, "maxLength": 1000, "description": "The request in plain words."},
	}, []string{"request"})
	use.Name = "memory.workflow.use"
	use.Description = "Get one saved workflow's instructions filled in with its inputs, and the steps learned from earlier runs. Follow them now with your other tools; nothing runs by itself."
	use.InputSchema = agentToolSchema(map[string]any{
		"method": map[string]any{"type": "string", "minLength": 1, "maxLength": 200, "description": "Workflow id or exact title from memory_workflow_list or memory_workflow_find."},
		"inputs": map[string]any{"type": "object", "maxProperties": 12},
	}, []string{"method"})
	save := base
	save.Name, save.Risk, save.Idempotent, save.AuditEvent = "memory.workflow.save", agent.RiskWrite, false, "agent.method.saved"
	save.Description = "Save a reusable workflow for this agent, or update one by id. Use only when the user asked to save or change a workflow. " +
		"Inputs are named blanks the user fills each time it runs."
	save.InputSchema = agentToolSchema(map[string]any{
		"id":           map[string]any{"type": "string", "maxLength": 200},
		"title":        map[string]any{"type": "string", "minLength": 1, "maxLength": 120},
		"description":  map[string]any{"type": "string", "maxLength": 1000},
		"instructions": map[string]any{"type": "string", "minLength": 1, "maxLength": 6000},
		"inputs": map[string]any{"type": "array", "maxItems": 12, "items": map[string]any{
			"type": "object", "required": []string{"key", "label", "type"}, "additionalProperties": false,
			"properties": map[string]any{
				"key":      map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9_]{0,39}$"},
				"label":    map[string]any{"type": "string", "minLength": 1, "maxLength": 80},
				"type":     map[string]any{"type": "string", "enum": []string{"text", "choice", "number", "boolean"}},
				"required": map[string]any{"type": "boolean"},
				"options":  map[string]any{"type": "array", "maxItems": 20, "items": map[string]any{"type": "string", "maxLength": 80}},
			},
		}},
	}, []string{"title", "instructions"})
	learn := base
	learn.Name, learn.Risk, learn.Idempotent, learn.AuditEvent = "memory.workflow.learn", agent.RiskWrite, false, "agent.workflow.learned"
	learn.Description = "After a saved workflow ran well, record the steps that worked so the next run goes faster: which sources, pages, tools and order, and what to skip. " +
		"Write reusable steps, never the person's data or this run's results. Saves a new version; the person's own instructions stay unchanged."
	learn.InputSchema = agentToolSchema(map[string]any{
		"method": map[string]any{"type": "string", "minLength": 1, "maxLength": 200, "description": "Workflow id or exact title."},
		"steps":  map[string]any{"type": "string", "minLength": 1, "maxLength": 4000, "description": "Short numbered steps."},
	}, []string{"method", "steps"})
	resolve := func(ctx context.Context, invocation agenttools.Invocation, reference string) (db.AgentMethod, bool, error) {
		methods, err := agentMethodsFor(ctx, database, invocation)
		if err != nil {
			return db.AgentMethod{}, false, err
		}
		reference = strings.TrimSpace(reference)
		for _, method := range methods {
			if method.ID == reference || strings.EqualFold(strings.TrimSpace(method.Definition.Title), reference) {
				return method, true, nil
			}
		}
		return db.AgentMethod{}, false, nil
	}
	return []agenttools.Registration{
		{Descriptor: find, Handler: func(ctx context.Context, invocation agenttools.Invocation, request agent.ToolRequest) (json.RawMessage, error) {
			var input struct {
				Request string `json:"request"`
			}
			if json.Unmarshal(request.Arguments, &input) != nil || strings.TrimSpace(input.Request) == "" {
				return nil, agenttools.ErrArgumentsInvalid
			}
			methods, err := agentMethodsFor(ctx, database, invocation)
			if err != nil {
				return nil, err
			}
			if found := findWorkflowsByMeaning(ctx, database, search, invocation, methods, input.Request, 3); len(found) > 0 {
				return json.Marshal(map[string]any{"workflows": found})
			}
			// Vectors for new workflows may still be on their way.
			return json.Marshal(map[string]any{"workflows": matchWorkflows(methods, input.Request, 3)})
		}},
		{Descriptor: learn, Handler: func(ctx context.Context, invocation agenttools.Invocation, request agent.ToolRequest) (json.RawMessage, error) {
			var input struct{ Method, Steps string }
			if json.Unmarshal(request.Arguments, &input) != nil {
				return nil, agenttools.ErrArgumentsInvalid
			}
			method, found, err := resolve(ctx, invocation, input.Method)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, agent.ErrInvalidRequest("no enabled workflow " + input.Method + "; call memory_workflow_list")
			}
			lesson, err := database.AddAgentMethodLesson(ctx, invocation.UserID, method.ID, input.Steps, invocation.RunID)
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"workflow": method.Definition.Title, "version": lesson.Version})
		}},
		{Descriptor: save, Handler: func(ctx context.Context, invocation agenttools.Invocation, request agent.ToolRequest) (json.RawMessage, error) {
			return saveAgentMethod(ctx, database, invocation, request)
		}},
		{Descriptor: list, Handler: func(ctx context.Context, invocation agenttools.Invocation, _ agent.ToolRequest) (json.RawMessage, error) {
			methods, err := agentMethodsFor(ctx, database, invocation)
			if err != nil {
				return nil, err
			}
			items := make([]map[string]any, 0, len(methods))
			for _, method := range methods {
				items = append(items, map[string]any{"id": method.ID, "title": method.Definition.Title,
					"description": method.Definition.Description, "inputs": method.Definition.Inputs, "where": method.Definition.Target})
			}
			return json.Marshal(map[string]any{"methods": items})
		}},
		{Descriptor: use, Handler: func(ctx context.Context, invocation agenttools.Invocation, request agent.ToolRequest) (json.RawMessage, error) {
			var input struct {
				Method string         `json:"method"`
				Inputs map[string]any `json:"inputs"`
			}
			if json.Unmarshal(request.Arguments, &input) != nil {
				return nil, agenttools.ErrArgumentsInvalid
			}
			methods, err := agentMethodsFor(ctx, database, invocation)
			if err != nil {
				return nil, err
			}
			reference := strings.TrimSpace(input.Method)
			for _, method := range methods {
				if method.ID != reference && !strings.EqualFold(strings.TrimSpace(method.Definition.Title), reference) {
					continue
				}
				instructions, err := db.RenderAgentMethod(method.Definition, input.Inputs)
				if err != nil {
					return nil, agent.ErrInvalidRequest("those inputs do not fit " + method.Definition.Title + "; check its inputs in memory_workflow_list")
				}
				result := map[string]any{"method": method.Definition.Title, "instructions": instructions}
				if lesson, err := database.LatestAgentMethodLesson(ctx, invocation.UserID, method.ID); err == nil && lesson.Version > 0 {
					result["learned_steps"] = lesson.Steps
					result["learned_version"] = lesson.Version
					result["learned_note"] = "Steps that worked on earlier runs. Follow them where they still fit; if one no longer works, adapt, and save what worked with memory_workflow_learn."
				}
				if method.Definition.Target != "cloud" {
					result["note"] = "This method was saved to run in a Misty browser window. Do what you can with your tools, or tell the user to start it from the Agents page."
				}
				return json.Marshal(result)
			}
			return nil, agent.ErrInvalidRequest("no enabled method " + reference + "; call memory_workflow_list")
		}},
	}
}

// agentMethodsFor returns the run's agent's enabled workflow methods in the
// run's Space.
func agentMethodsFor(ctx context.Context, database *db.Database, invocation agenttools.Invocation) ([]db.AgentMethod, error) {
	if invocation.AgentID == "" {
		return []db.AgentMethod{}, nil
	}
	space, err := runMemorySpace(ctx, database, invocation)
	if err != nil {
		return nil, err
	}
	methods, err := database.AgentMethods(ctx, invocation.UserID, space, invocation.AgentID)
	if err != nil {
		return nil, err
	}
	enabled := methods[:0]
	for _, method := range methods {
		if method.Enabled && method.Kind == "workflow" {
			enabled = append(enabled, method)
		}
	}
	return enabled, nil
}

func saveAgentMethod(ctx context.Context, database *db.Database, invocation agenttools.Invocation, request agent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		ID           string                `json:"id"`
		Title        string                `json:"title"`
		Description  string                `json:"description"`
		Instructions string                `json:"instructions"`
		Inputs       []db.AgentMethodInput `json:"inputs"`
	}
	if json.Unmarshal(request.Arguments, &input) != nil {
		return nil, agenttools.ErrArgumentsInvalid
	}
	if invocation.AgentID == "" {
		return nil, agent.ErrInvalidRequest("memory_workflow_save is available only to agents")
	}
	if input.Inputs == nil {
		input.Inputs = []db.AgentMethodInput{}
	}
	space, err := runMemorySpace(ctx, database, invocation)
	if err != nil {
		return nil, err
	}
	method := db.AgentMethod{SpaceID: space, AgentID: invocation.AgentID, Kind: "workflow", Enabled: true, Definition: db.AgentMethodDefinition{
		Title: input.Title, Description: input.Description, Instructions: input.Instructions, Inputs: input.Inputs, Target: "cloud", RequiredTools: []string{},
	}}
	expected := 0
	if id := strings.TrimSpace(input.ID); id != "" {
		existing, err := database.AgentMethods(ctx, invocation.UserID, space, invocation.AgentID)
		if err != nil {
			return nil, err
		}
		for _, current := range existing {
			if current.ID == id {
				method.ID, method.Enabled, method.Definition.Target, expected = current.ID, current.Enabled, current.Definition.Target, current.Version
			}
		}
		if expected == 0 {
			return nil, agent.ErrInvalidRequest("no method " + id + " for this agent; call memory_workflow_list")
		}
	}
	if err := db.ValidateAgentMethod(method.Definition); err != nil {
		return nil, agent.ErrInvalidRequest("a method needs a title under 120 characters, instructions under 6000 and at most 12 inputs")
	}
	saved, err := database.SaveAgentMethod(ctx, invocation.UserID, method, expected)
	if err != nil {
		return nil, agent.ErrInvalidRequest("the method could not be saved; it may have changed, so read it again with memory_workflow_list")
	}
	return json.Marshal(map[string]any{"method": map[string]any{"id": saved.ID, "title": saved.Definition.Title, "version": saved.Version}})
}
