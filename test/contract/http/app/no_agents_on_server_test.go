package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	. "github.com/misty-org/misty-server/internal/app"
	"github.com/misty-org/misty-server/internal/platform/db"
)

var removedRoutePrefixes = []string{
	"/mcp", "/internal/agent-runtime", "/ai", "/misty", "/integrations",
	"/agent-runs", "/agent-mentions", "/agent-requests",
	"/me/agent-", "/me/conversations", "/me/app-requests", "/me/memory",
}

var removedRouteParts = []string{"/agent-runtime", "/embeddings", "/workflow-node-jobs", "/agent-execution"}

var forbiddenRouteWord = regexp.MustCompile(`(?i)(^|[-_])(prompt|prompts|completion|completions|invocation|invocations|mcp|toolbox|tool-result|tool-results|tool-output|model-output)($|[-_])`)

var forbiddenRequestField = regexp.MustCompile(`^(systemprompt|prompt|prompts|toolresult|toolresults|tooloutput|toolcall|toolcalls|modeloutput|modelresponse|modelturn|completion|assistantmessage|assistanttext|runtimeevent|agentrunkey|runkey|apikey|apikeys|secretkey|secret|credential|credentials|connector|connectors|connectedapps|mcpserver|mcpservers|plugin|plugins|skill|skills|memory|memories|enginememory|enginesignin)$`)

var agentDefinitionFields = map[string]bool{
	"space_id": true, "space_name": true, "agent_id": true, "agent_name": true, "owner_user_id": true, "owner_name": true,
	"added_at": true, "visibility": true, "name": true, "role": true, "description": true, "instructions": true, "soul": true,
	"icon": true, "avatar": true, "voice_id": true, "engine_id": true, "engine_model": true, "reasoning_effort": true,
	"origin": true, "revision": true, "updated_at": true, "updated_by_user_id": true, "can_edit": true,
}

func routeProblem(path string) string {
	normalized := strings.TrimPrefix(strings.TrimPrefix(path, "/api/"), "/v1/")
	normalized = "/" + strings.TrimLeft(normalized, "/")
	for _, prefix := range removedRoutePrefixes {
		trimmed := strings.TrimSuffix(prefix, "-")
		if normalized == trimmed || strings.HasPrefix(normalized, trimmed+"/") || (prefix != trimmed && strings.HasPrefix(normalized, prefix)) {
			return "removed prefix " + prefix
		}
	}
	for _, part := range removedRouteParts {
		if strings.Contains(normalized, part+"/") || strings.HasSuffix(normalized, part) {
			return "removed path " + part
		}
	}
	for _, segment := range strings.Split(normalized, "/") {
		if !strings.HasPrefix(segment, "{") && forbiddenRouteWord.MatchString(segment) {
			return "takes a model's work: " + segment
		}
	}
	return ""
}

func jsonFieldProblem(name string) bool {
	normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(name))
	return forbiddenRequestField.MatchString(normalized)
}

func TestNoAgentsChecksCatchWhatTheyClaimTo(t *testing.T) {
	for _, path := range []string{
		"/mcp", "/v1/mcp", "/internal/agent-runtime/runs/{runID}/events", "/api/ai/invocations", "/ai/status",
		"/misty/conversations", "/me/agent-grants", "/v1/me/memory/tree", "/integrations/apps",
		"/agent-runs/{runID}", "/devices/{deviceID}/agent-runtime/deliveries/claim", "/devices/{deviceID}/embeddings/claim",
		"/devices/{deviceID}/workflow-node-jobs/claim", "/spaces/{spaceID}/prompts", "/runs/{id}/tool-results",
	} {
		if routeProblem(path) == "" {
			t.Errorf("%s passed the route check", path)
		}
	}
	for _, path := range []string{
		"/spaces/{spaceID}/agents/{agentID}", "/me/space-agents", "/me/settings", "/spaces/{spaceID}/agent-artifacts",
		"/api/spaces/{spaceID}/messages", "/me/voice/transcripts", "/server/alerts", "/auth/google",
	} {
		if problem := routeProblem(path); problem != "" {
			t.Errorf("%s failed the route check: %s", path, problem)
		}
	}
	for _, field := range []string{
		"prompt", "system_prompt", "toolResults", "tool_output", "model-output", "toolCalls", "completion",
		"api_key", "apiKey", "connectors", "mcp_servers", "plugins", "skills", "memory", "engine_memory", "credentials",
	} {
		if !jsonFieldProblem(field) {
			t.Errorf("%s passed the field check", field)
		}
	}
	for _, field := range []string{
		"name", "text", "result", "content", "agent_id", "agent_name",
		"instructions", "soul", "voice_id", "engine_id", "engine_model", "reasoning_effort", "avatar", "revision",
	} {
		if jsonFieldProblem(field) {
			t.Errorf("%s failed the field check", field)
		}
	}
}

func TestServerMountsNoRouteForAModelsWork(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "")
	t.Setenv("MISTY_OPERATOR_TOKEN", "no-agents")
	server, err := CreateServer()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.MountHandlers(); err != nil {
		t.Fatal(err)
	}
	if err := chi.Walk(server.Router, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if problem := routeProblem(path); problem != "" {
			t.Errorf("%s %s: %s", method, path, problem)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRouteGoldenHasNoRemovedRoutes(t *testing.T) {
	raw, err := os.ReadFile("routes.golden")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(strings.ReplaceAll(string(raw), "\r\n", "\n")), "\n") {
		_, path, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("malformed line %q", line)
		}
		if problem := routeProblem(path); problem != "" {
			t.Errorf("routes.golden has %s: %s", line, problem)
		}
	}
}

func TestServerHandlersDeclareNoFieldForAModelsWork(t *testing.T) {
	for _, dir := range []string{"../../../../internal/platform/httpapi", "../../../../internal/platform/db"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no sources in %s: %v", dir, err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				field, ok := node.(*ast.Field)
				if !ok || field.Tag == nil {
					return true
				}
				tag, err := strconv.Unquote(field.Tag.Value)
				if err != nil {
					t.Fatalf("%s: tag %s: %v", file, field.Tag.Value, err)
				}
				name, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
				if name != "" && jsonFieldProblem(name) {
					t.Errorf("%s declares the JSON field %q", filepath.Base(file), name)
				}
				return true
			})
		}
	}
}

func TestAnAgentsDefinitionCarriesOnlyItsDefinition(t *testing.T) {
	for _, shape := range []reflect.Type{reflect.TypeOf(db.SpaceAgent{}), reflect.TypeOf(db.SpaceAgentDefinition{})} {
		for index := 0; index < shape.NumField(); index++ {
			name, _, _ := strings.Cut(shape.Field(index).Tag.Get("json"), ",")
			if name == "" || name == "-" {
				t.Errorf("%s.%s has no JSON name", shape.Name(), shape.Field(index).Name)
				continue
			}
			if !agentDefinitionFields[name] || jsonFieldProblem(name) {
				t.Errorf("%s declares %q, which isn't part of an agent's definition", shape.Name(), name)
			}
		}
	}
}

var sharedThreadFields = map[string]bool{
	"id": true, "space_id": true, "agent_id": true, "owner_user_id": true, "owner_name": true,
	"title": true, "state": true, "continued_from": true, "entries": true, "created_at": true,
	"updated_at": true, "key": true, "seq": true, "kind": true, "body": true, "reactions": true,
	"emoji": true, "count": true, "mine": true,
}

func TestASharedThreadCarriesOnlyWhatMembersSee(t *testing.T) {
	for _, shape := range []reflect.Type{
		reflect.TypeOf(db.AgentThread{}),
		reflect.TypeOf(db.AgentThreadEntry{}),
		reflect.TypeOf(db.AgentThreadEntryInput{}),
		reflect.TypeOf(db.AgentThreadReaction{}),
	} {
		for index := 0; index < shape.NumField(); index++ {
			name, _, _ := strings.Cut(shape.Field(index).Tag.Get("json"), ",")
			if name == "" || name == "-" {
				t.Errorf("%s.%s has no JSON name", shape.Name(), shape.Field(index).Name)
				continue
			}
			if !sharedThreadFields[name] || jsonFieldProblem(name) {
				t.Errorf("%s declares %q, which members of a Space never see of a thread", shape.Name(), name)
			}
		}
	}
}
