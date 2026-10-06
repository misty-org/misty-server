package api

import (
	"context"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Methods are saved workflows owned by one agent. The agent reads a method's
// rendered instructions and follows them in its current run, so a method
// never starts a second run or grants a tool.
func methodToolRegistrations(database *db.Database) []agenttools.Registration {
	base := agenttools.Descriptor{Version: 1, OutputSchema: agentToolObjectOutputSchema(), Locality: agenttools.LocalityServer,
		AllowCustomAgent: true, Approval: agenttools.ApprovalNone, Risk: serveragent.RiskRead, Idempotent: true}
	list, use := base, base
	list.Name = "methods.list"
	list.Description = "List the saved methods (reusable workflows) this agent has, with the inputs each one takes."
	list.InputSchema = agentToolSchema(map[string]any{}, nil)
	use.Name = "methods.use"
	use.Description = "Get one saved method's instructions filled in with its inputs. Follow them now with your other tools; nothing runs by itself."
	use.InputSchema = agentToolSchema(map[string]any{
		"method": map[string]any{"type": "string", "minLength": 1, "maxLength": 200, "description": "Method id or exact title from methods_list."},
		"inputs": map[string]any{"type": "object", "maxProperties": 12},
	}, []string{"method"})
	save := base
	save.Name, save.Risk, save.Idempotent, save.AuditEvent = "methods.save", serveragent.RiskWrite, false, "agent.method.saved"
	save.Description = "Save a reusable method for this agent, or update one by id. Use only when the user asked to save or change a method. " +
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
	return []agenttools.Registration{
		{Descriptor: save, Handler: func(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
			return saveAgentMethod(ctx, database, invocation, request)
		}},
		{Descriptor: list, Handler: func(ctx context.Context, invocation agenttools.Invocation, _ serveragent.ToolRequest) (json.RawMessage, error) {
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
		{Descriptor: use, Handler: func(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
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
					return nil, serveragent.ErrInvalidRequest("those inputs do not fit " + method.Definition.Title + "; check its inputs in methods_list")
				}
				result := map[string]any{"method": method.Definition.Title, "instructions": instructions}
				if method.Definition.Target != "cloud" {
					result["note"] = "This method was saved to run in a Misty browser window. Do what you can with your tools, or tell the user to start it from the Agents page."
				}
				return json.Marshal(result)
			}
			return nil, serveragent.ErrInvalidRequest("no enabled method " + reference + "; call methods_list")
		}},
	}
}

// agentMethodsFor returns the run's agent's enabled workflow methods.
func agentMethodsFor(ctx context.Context, database *db.Database, invocation agenttools.Invocation) ([]db.AgentMethod, error) {
	if invocation.AgentID == "" {
		return []db.AgentMethod{}, nil
	}
	methods, err := database.AgentMethods(ctx, invocation.UserID, invocation.AgentID)
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

func saveAgentMethod(ctx context.Context, database *db.Database, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
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
		return nil, serveragent.ErrInvalidRequest("methods_save is available only to agents")
	}
	if input.Inputs == nil {
		input.Inputs = []db.AgentMethodInput{}
	}
	method := db.AgentMethod{AgentID: invocation.AgentID, Kind: "workflow", Enabled: true, Definition: db.AgentMethodDefinition{
		Title: input.Title, Description: input.Description, Instructions: input.Instructions, Inputs: input.Inputs, Target: "cloud", RequiredTools: []string{},
	}}
	expected := 0
	if id := strings.TrimSpace(input.ID); id != "" {
		existing, err := database.AgentMethods(ctx, invocation.UserID, invocation.AgentID)
		if err != nil {
			return nil, err
		}
		for _, current := range existing {
			if current.ID == id {
				method.ID, method.Enabled, method.Definition.Target, expected = current.ID, current.Enabled, current.Definition.Target, current.Version
			}
		}
		if expected == 0 {
			return nil, serveragent.ErrInvalidRequest("no method " + id + " for this agent; call methods_list")
		}
	}
	if err := db.ValidateAgentMethod(method.Definition); err != nil {
		return nil, serveragent.ErrInvalidRequest("a method needs a title under 120 characters, instructions under 6000 and at most 12 inputs")
	}
	saved, err := database.SaveAgentMethod(ctx, invocation.UserID, method, expected)
	if err != nil {
		return nil, serveragent.ErrInvalidRequest("the method could not be saved; it may have changed, so read it again with methods_list")
	}
	return json.Marshal(map[string]any{"method": map[string]any{"id": saved.ID, "title": saved.Definition.Title, "version": saved.Version}})
}
