package api

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

// Space data tools act inside one Space. Account-level runs use the same tool
// names with an optional `space` argument: reads without one cover every Space
// the owner can access, creates default to the personal Space, and changes to
// existing items name the Space returned with them.
const maxRoutedReadSpaces = 25

type globalSpaceCallKey struct{}

// globalSpaceCall keeps a routed call's effect identity stable in the journal.
type globalSpaceCall struct {
	spaceID   string
	sessionID string
}

func spaceDataTool(name string) bool {
	switch strings.Split(name, ".")[0] {
	case "context", "members", "messages", "library", "tasks", "calendar", "notes", "drawings", "roadmaps", "roadmap", "threads":
		return true
	}
	return false
}

// spaceDataToolDescriptors are the Space-bound Misty data tools.
func spaceDataToolDescriptors() []agenttools.Descriptor {
	descriptors := []agenttools.Descriptor{
		contextGetToolDescriptor(), membersListToolDescriptor(), membersResolveToolDescriptor(),
		messagesSearchToolDescriptor(), messagesSendToolDescriptor(), librarySearchToolDescriptor(),
		tasksQueryToolDescriptor(), calendarQueryToolDescriptor(), tasksCreateToolDescriptor(), tasksUpdateToolDescriptor(),
	}
	descriptors = append(descriptors, noteAgentToolDescriptors()...)
	descriptors = append(descriptors, drawingAgentToolDescriptors()...)
	descriptors = append(descriptors, calendarWriteToolDescriptors()...)
	descriptors = append(descriptors, roadmapAgentToolDescriptors()...)
	return append(descriptors, libraryMutationToolDescriptors()...)
}

// routedSpaceRegistrations exposes every Space data tool to an account-level
// run, plus spaces.list for naming destinations. context.get is omitted because
// the run context already states the time and timezone.
func routedSpaceRegistrations(database *db.Database, except ...string) []agenttools.Registration {
	registrations := []agenttools.Registration{{Descriptor: spacesListToolDescriptor(), Handler: func(ctx context.Context, invocation agenttools.Invocation, _ serveragent.ToolRequest) (json.RawMessage, error) {
		return listAgentSpaces(ctx, database, invocation)
	}}}
	for _, descriptor := range spaceDataToolDescriptors() {
		if descriptor.Name == toolboxContextGet || slices.Contains(except, descriptor.Name) {
			continue
		}
		if registration, ok := routeSpaceDescriptor(database, descriptor); ok {
			registrations = append(registrations, registration)
		}
	}
	return registrations
}

func spacesListToolDescriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: "spaces.list", Version: 1, Risk: serveragent.RiskRead, Approval: agenttools.ApprovalNone,
		Description: "List the Spaces this account can access, including the personal Space. Use a Space's name or id as the `space` argument of other tools.",
		InputSchema: TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{}, "required": []string{}, "additionalProperties": false}),
		OutputSchema: agentToolObjectOutputSchema(), Locality: agenttools.LocalityServer, AllowCustomAgent: true, Idempotent: true,
	}
}

func listAgentSpaces(ctx context.Context, database *db.Database, invocation agenttools.Invocation) (json.RawMessage, error) {
	spaces, err := database.ListSpaces(ctx, invocation.UserID)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(spaces))
	for _, space := range spaces {
		items = append(items, map[string]any{"id": space.ID, "name": space.Name, "personal": space.IsDefault && space.OwnerUserID == invocation.UserID, "shared": space.IsShared})
	}
	return json.Marshal(map[string]any{"spaces": items})
}

func routeSpaceDescriptor(database *db.Database, descriptor agenttools.Descriptor) (agenttools.Registration, bool) {
	var schema map[string]any
	if json.Unmarshal(descriptor.InputSchema, &schema) != nil || schema["type"] != "object" {
		return agenttools.Registration{}, false
	}
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		properties = map[string]any{}
	}
	if _, taken := properties["space"]; taken {
		return agenttools.Registration{}, false
	}
	read := descriptor.Risk == serveragent.RiskRead
	create := strings.HasSuffix(descriptor.Name, ".create")
	hint := "Space name or id. Omit to search every Space you can access."
	switch {
	case create:
		hint = "Space name or id. Omit to use your personal Space."
	case !read:
		hint = "Space name or id of the item or audience, as returned with it."
	}
	properties["space"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 200, "description": hint}
	schema["properties"] = properties
	if !read && !create {
		required, _ := schema["required"].([]any)
		schema["required"] = append(required, "space")
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return agenttools.Registration{}, false
	}
	routed := descriptor
	routed.InputSchema = encoded
	routed.Description = strings.ReplaceAll(descriptor.Description, "the current Space", "a Space")
	routed.RequiredPermission, routed.AgentPermission, routed.OwnerOnly = "", "", false
	routed.Locality = agenttools.LocalityRouted
	routed.Sources, routed.Triggers, routed.Aliases = nil, nil, nil
	return agenttools.Registration{Descriptor: routed, Handler: routedSpaceHandler(database, routed.Name, read, create)}, true
}

func routedSpaceHandler(database *db.Database, name string, read, create bool) agenttools.Handler {
	return func(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
		arguments, reference, err := splitSpaceArgument(request.Arguments)
		if err != nil {
			return nil, err
		}
		spaces, err := database.ListSpaces(ctx, invocation.UserID)
		if err != nil {
			return nil, err
		}
		targets, err := routedSpaceTargets(spaces, invocation.UserID, reference, read, create)
		if err != nil {
			return nil, err
		}
		toolbox := spaceAgentToolbox(database)
		inner := serveragent.ToolRequest{ID: request.ID, Name: name, Arguments: arguments}
		if len(targets) == 1 {
			return executeToolInSpace(ctx, database, toolbox, invocation, targets[0], inner)
		}
		return fanOutSpaceRead(ctx, database, toolbox, invocation, targets, inner)
	}
}

func splitSpaceArgument(raw json.RawMessage) (json.RawMessage, string, error) {
	values := map[string]json.RawMessage{}
	if len(raw) > 0 && json.Unmarshal(raw, &values) != nil {
		return nil, "", agenttools.ErrArgumentsInvalid
	}
	reference := ""
	if value, ok := values["space"]; ok {
		if json.Unmarshal(value, &reference) != nil {
			return nil, "", agenttools.ErrArgumentsInvalid
		}
		delete(values, "space")
	}
	arguments, err := json.Marshal(values)
	return arguments, strings.TrimSpace(reference), err
}

func routedSpaceTargets(spaces []db.Space, userID, reference string, read, create bool) ([]db.Space, error) {
	if reference != "" {
		matches := []db.Space{}
		for _, space := range spaces {
			if space.ID == reference {
				return []db.Space{space}, nil
			}
			if strings.EqualFold(strings.TrimSpace(space.Name), reference) {
				matches = append(matches, space)
			}
		}
		switch len(matches) {
		case 1:
			return matches, nil
		case 0:
			return nil, serveragent.ErrInvalidRequest("unknown space " + strconv.Quote(reference) + "; available: " + spaceChoices(spaces))
		default:
			return nil, serveragent.ErrInvalidRequest("several Spaces are named " + strconv.Quote(reference) + "; pass one id: " + spaceChoices(matches))
		}
	}
	if len(spaces) == 0 {
		return nil, serveragent.ErrInvalidRequest("this account has no Spaces yet")
	}
	if read {
		return spaces[:min(len(spaces), maxRoutedReadSpaces)], nil
	}
	if create {
		for _, space := range spaces {
			if space.IsDefault && space.OwnerUserID == userID {
				return []db.Space{space}, nil
			}
		}
	}
	return nil, serveragent.ErrInvalidRequest("pass space for this change; available: " + spaceChoices(spaces))
}

func spaceChoices(spaces []db.Space) string {
	labels := make([]string, 0, min(len(spaces), 20))
	for _, space := range spaces[:min(len(spaces), 20)] {
		labels = append(labels, strconv.Quote(space.Name)+" ("+space.ID+")")
	}
	return strings.Join(labels, ", ")
}

// executeToolInSpace runs one Space tool with the owner's permissions in that
// Space. The Space toolbox authorizes and journals the effect.
func executeToolInSpace(ctx context.Context, database *db.Database, toolbox *agenttools.Registry, invocation agenttools.Invocation, space db.Space, request serveragent.ToolRequest) (json.RawMessage, error) {
	target := agenttools.Invocation{
		UserID: invocation.UserID, SpaceID: space.ID, AgentID: invocation.AgentID, RunID: invocation.RunID,
		Source: "space_conversation", Trigger: "message", OriginalInput: invocation.OriginalInput,
		ConversationScopeKind: db.ConversationScopeEveryone,
	}
	call := globalSpaceCall{spaceID: space.ID, sessionID: invocation.SessionID}
	return executeSpaceAgentToolbox(context.WithValue(ctx, globalSpaceCallKey{}, call), toolbox, target, database, request)
}

func fanOutSpaceRead(ctx context.Context, database *db.Database, toolbox *agenttools.Registry, invocation agenttools.Invocation, spaces []db.Space, request serveragent.ToolRequest) (json.RawMessage, error) {
	type spaceResult struct {
		SpaceID   string          `json:"space_id"`
		SpaceName string          `json:"space_name"`
		Result    json.RawMessage `json:"result,omitempty"`
		Error     string          `json:"error,omitempty"`
	}
	results := make([]spaceResult, 0, len(spaces))
	succeeded := 0
	var firstErr error
	for _, space := range spaces {
		result, err := executeToolInSpace(ctx, database, toolbox, invocation, space, request)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if errors.Is(err, workflowv2.ErrCapabilityDenied) || errors.Is(err, db.ErrSpaceForbidden) || errors.Is(err, db.ErrSpaceNotFound) {
			continue // This Space does not grant that read; it is not a failure.
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			results = append(results, spaceResult{SpaceID: space.ID, SpaceName: space.Name, Error: truncateAgentRuntimeText(err.Error(), 300)})
			continue
		}
		succeeded++
		results = append(results, spaceResult{SpaceID: space.ID, SpaceName: space.Name, Result: result})
	}
	if succeeded == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, workflowv2.ErrCapabilityDenied
	}
	return json.Marshal(map[string]any{"spaces": results})
}
