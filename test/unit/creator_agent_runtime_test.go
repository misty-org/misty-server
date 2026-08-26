package unit

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/agenttools"
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestAgentRuntimeSignatureCoversRequestAndRotates(t *testing.T) {
	current := bytes.Repeat([]byte{3}, 32)
	previous := bytes.Repeat([]byte{5}, 32)
	body := []byte(`{"runtime_run_id":"workflow_1"}`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	path := "/internal/agent-runtime/runs/run_1/context"
	signature := api.TestingAgentRuntimeSignature(previous, http.MethodPost, path, timestamp, body)
	if !api.TestingAgentRuntimeSignatureVerifies(current, previous, http.MethodPost, path, timestamp, signature, body) {
		t.Fatal("previous rotation secret should verify")
	}
	if api.TestingAgentRuntimeSignatureVerifies(current, previous, http.MethodPut, path, timestamp, signature, body) {
		t.Fatal("changed method should invalidate the signature")
	}
	if api.TestingAgentRuntimeSignatureVerifies(current, previous, http.MethodPost, path, timestamp, signature, []byte(`{}`)) {
		t.Fatal("changed body should invalidate the signature")
	}
	stale := strconv.FormatInt(time.Now().Add(-6*time.Minute).Unix(), 10)
	staleSignature := api.TestingAgentRuntimeSignature(current, http.MethodPost, path, stale, body)
	if api.TestingAgentRuntimeSignatureVerifies(current, nil, http.MethodPost, path, stale, staleSignature, body) {
		t.Fatal("stale signature should be rejected")
	}
}

func TestAgentRuntimeIsAlwaysWorkflowWhenConfigured(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "")
	t.Setenv("MISTY_AGENT_RUNTIME_URL", "https://runtime.test")
	t.Setenv("MISTY_AGENT_RUNTIME_INTERNAL_API_URL", "https://api.test")
	t.Setenv("MISTY_AGENT_RUNTIME_CONTROL_SECRET", "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	config, err := api.AgentRuntimeConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !config.Enabled() || config.Kind != "vercel-workflow" {
		t.Fatalf("runtime kind=%q enabled=%v", config.Kind, config.Enabled())
	}
}

func TestAgentRuntimeRejectsInsecurePublicControlPlaneURL(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	t.Setenv("MISTY_AGENT_RUNTIME_URL", "https://runtime.test")
	t.Setenv("MISTY_AGENT_RUNTIME_INTERNAL_API_URL", "http://api.example.com")
	t.Setenv("MISTY_AGENT_RUNTIME_CONTROL_SECRET", "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	if _, err := api.AgentRuntimeConfigFromEnv(); err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
		t.Fatalf("expected insecure control-plane URL to fail, got %v", err)
	}
}

func TestAgentRuntimeMayBeUnconfiguredOutsideProduction(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "")
	t.Setenv("MISTY_AGENT_RUNTIME_URL", "")
	t.Setenv("MISTY_AGENT_RUNTIME_CONTROL_SECRET", "")
	config, err := api.AgentRuntimeConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.Enabled() || config.Kind != "vercel-workflow" {
		t.Fatalf("runtime kind=%q enabled=%v", config.Kind, config.Enabled())
	}
}

func TestCreatorAgentRunModeMatrix(t *testing.T) {
	tests := []struct {
		mode, impact string
		want         bool
	}{
		{"ask", "observe", false}, {"ask", "routine", true}, {"ask", "consequential", true}, {"ask", "dangerous", true},
		{"auto", "observe", false}, {"auto", "routine", false}, {"auto", "consequential", true}, {"auto", "dangerous", true},
		{"full", "observe", false}, {"full", "routine", false}, {"full", "consequential", false}, {"full", "dangerous", true},
	}
	for _, test := range tests {
		if got := api.TestingCompanionToolNeedsApproval(test.mode, test.impact); got != test.want {
			t.Errorf("mode=%s impact=%s: approval=%v, want %v", test.mode, test.impact, got, test.want)
		}
	}
	for _, name := range []string{"git.push", "files.delete", "members.update", "browser.click", "browser.confirm_high_risk", "terminal.execute_unsandboxed"} {
		if got := api.TestingCompanionToolImpact(name); got != "dangerous" {
			t.Errorf("%s classified as %s", name, got)
		}
	}
	for _, name := range []string{"messages.send", "git.commit", "project.publish", "connections.write", "provider.slack.write"} {
		if got := api.TestingCompanionToolImpact(name); got != "consequential" {
			t.Errorf("%s classified as %s", name, got)
		}
	}
}

func TestCreatorApprovalSummaryDescribesTheProposedAction(t *testing.T) {
	summary := api.TestingCompanionToolApprovalSummary("tasks.create", json.RawMessage(`{"title":"Prepare investor demo"}`))
	if !strings.Contains(summary, "Prepare investor demo") || strings.Contains(summary, "tasks.create") {
		t.Fatalf("task approval summary = %q", summary)
	}
	message := api.TestingCompanionToolApprovalSummary("messages.send", json.RawMessage(`{"message":"The demo is ready"}`))
	if !strings.Contains(message, "Space chat") || !strings.Contains(message, "The demo is ready") {
		t.Fatalf("message approval summary = %q", message)
	}
}

func TestAgentLifecycleEventsRedactSecretsAndBoundText(t *testing.T) {
	raw := json.RawMessage(`{"authorization":"Bearer private","nested":{"api_key":"key","message":"safe"}}`)
	sanitized := string(api.TestingSanitizeAgentLifecycleJSON(raw))
	if strings.Contains(sanitized, "Bearer private") || strings.Contains(sanitized, `"key"`) || !strings.Contains(sanitized, `"message":"safe"`) {
		t.Fatalf("sanitized lifecycle event = %s", sanitized)
	}
	long := strings.Repeat("x", 2_100)
	bounded := api.TestingSanitizeAgentLifecycleJSON(json.RawMessage(`{"value":"` + long + `"}`))
	if len(bounded) >= len(long)+20 || !strings.Contains(string(bounded), "…") {
		t.Fatalf("lifecycle text was not bounded: %d bytes", len(bounded))
	}
}

func TestCreatorAgentCompletionRequiresExplicitTaskDone(t *testing.T) {
	state, code, kind, valid := api.TestingPersonalAgentRuntimeCompletionOutcome("success", false, true, "")
	if !valid || state != "completed_with_errors" || code != "task_not_completed" || kind != "failure" {
		t.Fatalf("success without done = %q %q %q %v", state, code, kind, valid)
	}
	state, code, kind, valid = api.TestingPersonalAgentRuntimeCompletionOutcome("success", true, true, "")
	if !valid || state != "completed" || code != "" || kind != "result" {
		t.Fatalf("explicit completion = %q %q %q %v", state, code, kind, valid)
	}
	state, code, kind, valid = api.TestingPersonalAgentRuntimeCompletionOutcome("incomplete", true, false, "tool_execution_failed")
	if !valid || state != "completed_with_errors" || code != "tool_execution_failed" || kind != "failure" {
		t.Fatalf("direct tool failure = %q %q %q %v", state, code, kind, valid)
	}
}

func TestCreatorAuthorityPolicyAndBrowserCatalog(t *testing.T) {
	policy := json.RawMessage(`{"mode":"inherit_creator"}`)
	if !api.TestingPersonalAgentCapabilityAllowed(policy, "tasks.update", "write") || !api.TestingPersonalAgentCapabilityAllowed(policy, "browser.navigate", "write") {
		t.Fatal("creator authority should enable Space and Browser actions")
	}
	if api.TestingPersonalAgentCapabilityAllowed(json.RawMessage(`{"mode":"inherit_invoker"}`), "browser.navigate", "write") {
		t.Fatal("retired invoker policy must fail closed")
	}
	want := map[string]bool{"browser.inspect": true, "browser.navigate": true, "browser.click": true, "browser.downloads.list": true}
	for _, descriptor := range api.TestingPersonalAgentToolboxDescriptors() {
		if !strings.HasPrefix(descriptor.Name, "browser.") {
			continue
		}
		if !want[descriptor.Name] || descriptor.Locality != agenttools.LocalityDevice {
			t.Fatalf("unexpected Browser descriptor: %#v", descriptor)
		}
		delete(want, descriptor.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing Browser descriptors: %#v", want)
	}
}

func TestCompanionToolboxIncludesAuthoritativeContextAndMemberResolution(t *testing.T) {
	want := map[string]bool{"context.get": true, "members.list": true, "members.resolve": true}
	for _, descriptor := range api.TestingPersonalAgentToolboxDescriptors() {
		delete(want, descriptor.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing foundational descriptors: %#v", want)
	}
	intent := api.TestingCompileAgentIntent("Add a chore for Melissa to the planner")
	if !contains(intent, "tasks.create") {
		t.Fatalf("planner request did not expose task creation: %#v", intent)
	}
}

func TestAgentTaskDueDatesUseTheCreatorsTimezone(t *testing.T) {
	if got := api.TestingAgentToolTimezone(`{"instruction":"create a task","timezone":"America/Los_Angeles"}`); got != "America/Los_Angeles" {
		t.Fatalf("run timezone = %q", got)
	}
	if got := api.TestingAgentToolTimezone(`{"timezone":"Mars/Olympus"}`); got != "UTC" {
		t.Fatalf("invalid run timezone fallback = %q", got)
	}
	local, err := api.TestingParseAgentToolTime("2026-08-19T19:00", "America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := local.Format(time.RFC3339), "2026-08-20T02:00:00Z"; got != want {
		t.Fatalf("local due time = %s, want %s", got, want)
	}
	dateOnly, err := api.TestingParseAgentToolTime("2026-08-19", "America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := dateOnly.Format(time.RFC3339), "2026-08-20T06:59:00Z"; got != want {
		t.Fatalf("date-only due time = %s, want %s", got, want)
	}
	if _, err := api.TestingParseAgentToolTime("2026-08-19T19:00", "Mars/Olympus"); err == nil {
		t.Fatal("invalid timezone should be rejected")
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
