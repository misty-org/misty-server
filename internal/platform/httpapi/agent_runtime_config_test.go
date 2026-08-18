package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAgentRuntimeSignatureCoversMethodPathTimestampAndBody(t *testing.T) {
	secret := bytes.Repeat([]byte{7}, 32)
	body := []byte(`{"runtime_run_id":"workflow_1"}`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := signAgentRuntimeRequest(secret, http.MethodPost, "/internal/agent-runtime/runs/run_1/context", timestamp, body)
	request := httptest.NewRequest(http.MethodPost, "https://misty.test/internal/agent-runtime/runs/run_1/context", bytes.NewReader(body))
	request.Header.Set("X-Misty-Agent-Timestamp", timestamp)
	request.Header.Set("X-Misty-Agent-Signature", signature)
	config := AgentRuntimeConfig{Mode: "workflow", URL: "https://runtime.test", secret: secret}
	if !config.verifyRequest(request, body) {
		t.Fatal("expected the valid runtime signature to verify")
	}
	if config.verifyRequest(request, []byte(`{"runtime_run_id":"workflow_2"}`)) {
		t.Fatal("expected a changed body to invalidate the signature")
	}
	request.Method = http.MethodPut
	if config.verifyRequest(request, body) {
		t.Fatal("expected a changed method to invalidate the signature")
	}
}

func TestAgentRuntimeSignatureAcceptsPreviousSecretDuringRotation(t *testing.T) {
	current := bytes.Repeat([]byte{3}, 32)
	previous := bytes.Repeat([]byte{5}, 32)
	body := []byte(`{}`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	request := httptest.NewRequest(http.MethodPost, "https://misty.test/internal/agent-runtime/runs/run_1/events", bytes.NewReader(body))
	request.Header.Set("X-Misty-Agent-Timestamp", timestamp)
	request.Header.Set("X-Misty-Agent-Signature", signAgentRuntimeRequest(previous, request.Method, request.URL.EscapedPath(), timestamp, body))
	config := AgentRuntimeConfig{Mode: "workflow", URL: "https://runtime.test", secret: current, previousSecret: previous}
	if !config.verifyRequest(request, body) {
		t.Fatal("expected the previous secret to verify during rotation")
	}
}

func TestAgentRuntimeSignatureRejectsStaleTimestamp(t *testing.T) {
	secret := bytes.Repeat([]byte{9}, 32)
	body := []byte(`{}`)
	timestamp := strconv.FormatInt(time.Now().Add(-agentRuntimeMaxSkew-time.Second).Unix(), 10)
	request := httptest.NewRequest(http.MethodPost, "https://misty.test/internal/agent-runtime/runs/run_1/complete", bytes.NewReader(body))
	request.Header.Set("X-Misty-Agent-Timestamp", timestamp)
	request.Header.Set("X-Misty-Agent-Signature", signAgentRuntimeRequest(secret, request.Method, request.URL.EscapedPath(), timestamp, body))
	config := AgentRuntimeConfig{Mode: "workflow", URL: "https://runtime.test", secret: secret}
	if config.verifyRequest(request, body) {
		t.Fatal("expected a stale signature to be rejected")
	}
}

func TestAgentRuntimeCanaryAllowlists(t *testing.T) {
	config := AgentRuntimeConfig{
		Mode: "workflow", URL: "https://runtime.test", secret: bytes.Repeat([]byte{1}, 32),
		ownerAllowlist: agentRuntimeAllowlist("user_allowed"), agentAllowlist: agentRuntimeAllowlist("agent_allowed"),
	}
	if !config.EnabledFor("user_allowed", "agent_other") || !config.EnabledFor("user_other", "agent_allowed") {
		t.Fatal("expected either the owner or Agent canary flag to enable the workflow runtime")
	}
	if config.EnabledFor("user_other", "agent_other") {
		t.Fatal("expected a run outside both canary lists to stay on the legacy path")
	}
	config.ownerAllowlist, config.agentAllowlist = nil, nil
	if !config.EnabledFor("user_other", "agent_other") {
		t.Fatal("expected empty canary lists to enable the configured runtime globally")
	}
}

func TestPersonalAgentRuntimePromptsKeepAttachmentsOutOfSystemInstructions(t *testing.T) {
	membership := &db.SpaceAgentMembership{Name: "Planner", Instructions: "Be concise.", SpaceInstructions: "Report progress."}
	task := &db.SpaceTask{TaskKey: "TASK-7", Title: "Review launch", Status: "in_progress", Notes: "Check every item."}
	system, prompt := personalAgentRuntimePrompts(membership, task, "UNTRUSTED FILE CONTENT", "one file was truncated")
	for _, expected := range []string{"You are Planner", "Be concise.", "Report progress.", "explicitly call tasks.update_assigned", "Do not browse"} {
		if !strings.Contains(system, expected) {
			t.Fatalf("system prompt missing %q: %s", expected, system)
		}
	}
	if strings.Contains(system, "UNTRUSTED FILE CONTENT") {
		t.Fatal("attachment content must not be promoted into system instructions")
	}
	for _, expected := range []string{"TASK-7", "Review launch", "Check every item.", "UNTRUSTED FILE CONTENT", "one file was truncated"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("task prompt missing %q: %s", expected, prompt)
		}
	}
}

func TestPersonalAgentRuntimeCompletionRequiresExplicitDone(t *testing.T) {
	state, code, kind, valid := personalAgentRuntimeCompletionOutcome("success", false, "")
	if !valid || state != "completed_with_errors" || code != "task_not_completed" || kind != "failure" {
		t.Fatalf("success without done = %q %q %q %v", state, code, kind, valid)
	}
	state, code, kind, valid = personalAgentRuntimeCompletionOutcome("success", true, "")
	if !valid || state != "completed" || code != "" || kind != "result" {
		t.Fatalf("explicit completion = %q %q %q %v", state, code, kind, valid)
	}
	state, code, kind, valid = personalAgentRuntimeCompletionOutcome("failed", false, "provider_unavailable")
	if !valid || state != "failed" || code != "provider_unavailable" || kind != "failure" {
		t.Fatalf("runtime failure = %q %q %q %v", state, code, kind, valid)
	}
	if _, _, _, valid = personalAgentRuntimeCompletionOutcome("unknown", false, ""); valid {
		t.Fatal("unknown completion status must be rejected")
	}
}
