package unit

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
)

func TestAgentToolboxRejectsDuplicateNamesAndAliases(t *testing.T) {
	handler := func(context.Context, agenttools.Invocation, serveragent.ToolRequest) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}
	base := agenttools.Registration{Descriptor: agenttools.Descriptor{Name: "tasks.query", Description: "Query Tasks", Risk: serveragent.RiskRead, Aliases: []string{"tasks.list"}}, Handler: handler}
	if _, err := agenttools.New(base, base); !errors.Is(err, agenttools.ErrInvalidRegistration) {
		t.Fatalf("duplicate tool error = %v", err)
	}
	conflict := agenttools.Registration{Descriptor: agenttools.Descriptor{Name: "tasks.list", Description: "List Tasks", Risk: serveragent.RiskRead}, Handler: handler}
	if _, err := agenttools.New(base, conflict); !errors.Is(err, agenttools.ErrInvalidRegistration) {
		t.Fatalf("alias conflict error = %v", err)
	}
}

func TestAgentToolboxFiltersExplicitIntentAndReauthorizesExecution(t *testing.T) {
	executions := 0
	registry := agenttools.MustNew(agenttools.Registration{
		Descriptor: agenttools.Descriptor{
			Name: "spaces.rename", Description: "Rename the current Space", Risk: serveragent.RiskWrite,
			Approval: agenttools.ApprovalExplicitIntent, RequiredPermission: "spaces.owner", AuditEvent: "space.updated",
		},
		Handler: func(context.Context, agenttools.Invocation, serveragent.ToolRequest) (json.RawMessage, error) {
			executions++
			return json.RawMessage(`{"ok":true}`), nil
		},
	})
	allowed := true
	authorize := func(context.Context, agenttools.Invocation, agenttools.Descriptor) (bool, error) {
		return allowed, nil
	}

	manifest, err := registry.Resolve(context.Background(), agenttools.Invocation{}, []string{"spaces.rename"}, authorize)
	if err != nil || len(manifest.Tools) != 0 {
		t.Fatalf("implicit manifest = %#v, %v", manifest, err)
	}
	invocation := agenttools.Invocation{ExplicitTools: map[string]bool{"spaces.rename": true}}
	manifest, err = registry.Resolve(context.Background(), invocation, []string{"spaces.rename"}, authorize)
	if err != nil || !reflect.DeepEqual(manifest.Tools, []serveragent.ToolDefinition{{Name: "spaces.rename", Description: "Rename the current Space", Risk: serveragent.RiskWrite}}) {
		t.Fatalf("explicit manifest = %#v, %v", manifest, err)
	}

	allowed = false
	_, err = registry.Execute(context.Background(), invocation, serveragent.ToolRequest{Name: "spaces.rename", Arguments: json.RawMessage(`{"name":"Launch"}`)}, authorize)
	if !errors.Is(err, agenttools.ErrCapabilityDenied) || executions != 0 {
		t.Fatalf("execution error = %v, executions = %d", err, executions)
	}
}

func TestAgentToolboxAliasesExecuteCanonicalHandler(t *testing.T) {
	var executedName string
	registry := agenttools.MustNew(agenttools.Registration{
		Descriptor: agenttools.Descriptor{Name: "messages.search", Description: "Search messages", Risk: serveragent.RiskRead, Aliases: []string{"space.search_messages"}},
		Handler: func(_ context.Context, _ agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
			executedName = request.Name
			return json.RawMessage(`{"count":0}`), nil
		},
	})
	result, err := registry.Execute(context.Background(), agenttools.Invocation{}, serveragent.ToolRequest{Name: "space.search_messages", Arguments: json.RawMessage(`{}`)}, nil)
	if err != nil || executedName != "messages.search" || string(result) != `{"count":0}` {
		t.Fatalf("alias result = %s, name = %q, error = %v", result, executedName, err)
	}
}

func TestAgentToolboxUsesSourceApprovalAndDelegatesDurableApproval(t *testing.T) {
	executions := 0
	registry := agenttools.MustNew(agenttools.Registration{
		Descriptor: agenttools.Descriptor{
			Name: "tasks.update", Description: "Update a Task", Risk: serveragent.RiskWrite,
			Approval:         agenttools.ApprovalExplicitIntent,
			ApprovalBySource: map[string]agenttools.ApprovalPolicy{"canonical_run": agenttools.ApprovalInteractive},
			AuditEvent:       "task.updated",
		},
		Handler: func(context.Context, agenttools.Invocation, serveragent.ToolRequest) (json.RawMessage, error) {
			executions++
			return json.RawMessage(`{"ok":true}`), nil
		},
	})

	conversation := agenttools.Invocation{Source: "space_conversation"}
	manifest, err := registry.Resolve(context.Background(), conversation, []string{"tasks.update"}, nil)
	if err != nil || len(manifest.Tools) != 0 {
		t.Fatalf("implicit conversation manifest = %#v, %v", manifest, err)
	}
	canonical := agenttools.Invocation{Source: "canonical_run"}
	manifest, err = registry.Resolve(context.Background(), canonical, []string{"tasks.update"}, nil)
	if err != nil || len(manifest.Tools) != 1 {
		t.Fatalf("canonical manifest = %#v, %v", manifest, err)
	}
	_, err = registry.Execute(context.Background(), canonical, serveragent.ToolRequest{Name: "tasks.update", Arguments: json.RawMessage(`{}`)}, nil)
	if !errors.Is(err, agenttools.ErrApprovalRequired) || executions != 0 {
		t.Fatalf("interactive execution error = %v, executions = %d", err, executions)
	}
	canonical.DelegatedApproval = true
	_, err = registry.Execute(context.Background(), canonical, serveragent.ToolRequest{Name: "tasks.update", Arguments: json.RawMessage(`{}`)}, nil)
	if err != nil || executions != 1 {
		t.Fatalf("delegated execution error = %v, executions = %d", err, executions)
	}
}

func TestAgentToolboxEnforcesDeclaredInputAndOutputSchemas(t *testing.T) {
	registry := agenttools.MustNew(agenttools.Registration{
		Descriptor: agenttools.Descriptor{
			Name: "tasks.lookup", Description: "Look up a Task", Risk: serveragent.RiskRead,
			InputSchema:  json.RawMessage(`{"type":"object","required":["id"],"properties":{"id":{"type":"string","minLength":1}},"additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"type":"object","required":["found"],"properties":{"found":{"type":"boolean"}},"additionalProperties":false}`),
		},
		Handler: func(_ context.Context, _ agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
			if string(request.Arguments) == `{"id":"bad-result"}` {
				return json.RawMessage(`{"found":"yes"}`), nil
			}
			return json.RawMessage(`{"found":true}`), nil
		},
	})

	_, err := registry.Execute(context.Background(), agenttools.Invocation{}, serveragent.ToolRequest{Name: "tasks.lookup", Arguments: json.RawMessage(`{}`)}, nil)
	if !errors.Is(err, agenttools.ErrArgumentsInvalid) {
		t.Fatalf("missing required input error = %v", err)
	}
	_, err = registry.Execute(context.Background(), agenttools.Invocation{}, serveragent.ToolRequest{Name: "tasks.lookup", Arguments: json.RawMessage(`{"id":"one","extra":true}`)}, nil)
	if !errors.Is(err, agenttools.ErrArgumentsInvalid) {
		t.Fatalf("additional input error = %v", err)
	}
	_, err = registry.Execute(context.Background(), agenttools.Invocation{}, serveragent.ToolRequest{Name: "tasks.lookup", Arguments: json.RawMessage(`{"id":"bad-result"}`)}, nil)
	if !errors.Is(err, agenttools.ErrResultInvalid) {
		t.Fatalf("invalid output error = %v", err)
	}
	result, err := registry.Execute(context.Background(), agenttools.Invocation{}, serveragent.ToolRequest{Name: "tasks.lookup", Arguments: json.RawMessage(`{"id":"one"}`)}, nil)
	if err != nil || string(result) != `{"found":true}` {
		t.Fatalf("valid result = %s, %v", result, err)
	}
}

func TestAgentToolboxExecutionMiddlewareWrapsCanonicalValidatedAction(t *testing.T) {
	registry := agenttools.MustNew(agenttools.Registration{
		Descriptor: agenttools.Descriptor{
			Name: "tasks.update", Description: "Update a Task", Risk: serveragent.RiskWrite,
			Aliases: []string{"task.edit"}, AuditEvent: "task.updated",
			InputSchema: json.RawMessage(`{"type":"object","required":["id"]}`), OutputSchema: json.RawMessage(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`),
		},
		Handler: func(_ context.Context, _ agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
			if request.Name != "tasks.update" {
				t.Fatalf("handler request name = %q", request.Name)
			}
			return json.RawMessage(`{"ok":true}`), nil
		},
	})
	middlewareCalls := 0
	middleware := func(ctx context.Context, invocation agenttools.Invocation, descriptor agenttools.Descriptor, request serveragent.ToolRequest, next agenttools.Handler) (json.RawMessage, error) {
		middlewareCalls++
		if descriptor.Name != "tasks.update" || descriptor.AuditEvent != "task.updated" || request.Name != "tasks.update" {
			t.Fatalf("middleware descriptor=%#v request=%#v", descriptor, request)
		}
		return next(ctx, invocation, request)
	}
	result, err := registry.ExecuteWithMiddleware(context.Background(), agenttools.Invocation{}, serveragent.ToolRequest{Name: "task.edit", Arguments: json.RawMessage(`{"id":"task-1"}`)}, nil, middleware)
	if err != nil || middlewareCalls != 1 || string(result) != `{"ok":true}` {
		t.Fatalf("result=%s calls=%d err=%v", result, middlewareCalls, err)
	}
	invalidReplay := func(context.Context, agenttools.Invocation, agenttools.Descriptor, serveragent.ToolRequest, agenttools.Handler) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":"not-a-boolean"}`), nil
	}
	_, err = registry.ExecuteWithMiddleware(context.Background(), agenttools.Invocation{}, serveragent.ToolRequest{Name: "tasks.update", Arguments: json.RawMessage(`{"id":"task-1"}`)}, nil, invalidReplay)
	if !errors.Is(err, agenttools.ErrResultInvalid) {
		t.Fatalf("invalid replay error=%v", err)
	}
}
