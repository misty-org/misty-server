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
)

// Nothing AI runs on the server: an agent run starts, runs and ends on the
// person's computer. The server sees only what it would
// see from the person using the app by hand. These tests fail if it starts
// accepting a prompt, a tool result or a model's output again.

// removedRoutePrefixes are the route trees that left with the agents, after
// their /api or /v1 prefix. None comes back, under any of those prefixes.
var removedRoutePrefixes = []string{
	"/mcp", "/internal/agent-runtime", "/ai", "/misty", "/integrations",
	"/agent-runs", "/agent-mentions", "/agent-requests",
	"/me/agent-", "/me/conversations", "/me/app-requests", "/me/memory",
}

// removedRouteParts are the same in the middle of a path.
var removedRouteParts = []string{"/agent-runtime", "/embeddings", "/workflow-node-jobs", "/agent-execution"}

// forbiddenRouteWord matches a path segment that would take a model's work.
var forbiddenRouteWord = regexp.MustCompile(`(?i)(^|[-_])(prompt|prompts|completion|completions|invocation|invocations|mcp|toolbox|tool-result|tool-results|tool-output|model-output)($|[-_])`)

// forbiddenRequestField matches, with case and separators removed, a JSON field
// that would carry a model's work into or out of the server.
var forbiddenRequestField = regexp.MustCompile(`^(systemprompt|prompt|prompts|toolresult|toolresults|tooloutput|toolcall|toolcalls|modeloutput|modelresponse|modelturn|completion|assistantmessage|assistanttext|runtimeevent|agentrunkey|runkey)$`)

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

// The checks themselves: a test that can't fail proves nothing.
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
		"/spaces/{spaceID}/agents/{agentID}", "/me/space-agents", "/devices", "/spaces/{spaceID}/agent-artifacts",
		"/api/spaces/{spaceID}/messages", "/me/voice/transcripts", "/server/alerts", "/auth/google",
	} {
		if problem := routeProblem(path); problem != "" {
			t.Errorf("%s failed the route check: %s", path, problem)
		}
	}
	for _, field := range []string{"prompt", "system_prompt", "toolResults", "tool_output", "model-output", "toolCalls", "completion"} {
		if !jsonFieldProblem(field) {
			t.Errorf("%s passed the field check", field)
		}
	}
	for _, field := range []string{"name", "text", "result", "content", "agent_id", "agent_name"} {
		if jsonFieldProblem(field) {
			t.Errorf("%s failed the field check", field)
		}
	}
}

func TestServerMountsNoRouteForAModelsWork(t *testing.T) {
	configureJournalCollabForTest(t)
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

// Every JSON field in the handler packages, whichever way it travels: a struct a
// route could decode a prompt or a tool result into has no business here.
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
