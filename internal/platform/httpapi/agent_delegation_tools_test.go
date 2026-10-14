package api

import (
	"encoding/json"
	"strings"
	"testing"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestDelegatedTasksAreBoundedAndSelfContained(t *testing.T) {
	tasks, browser, err := parseDelegatedTasks(json.RawMessage(`{"tasks":[{"goal":"Find Acme's pricing","context":"Use acme.com","output_schema":{"type":"object","properties":{"price":{"type":"number"}}}},{"goal":"Find Globex's pricing"}]}`))
	if err != nil || len(tasks) != 2 || !browser || len(tasks[1].OutputSchema) != 0 {
		t.Fatal(tasks, browser, err)
	}
	if _, browser, _ := parseDelegatedTasks(json.RawMessage(`{"tasks":[{"goal":"x"}],"browser":false}`)); browser {
		t.Fatal("browser false was ignored")
	}
	for _, raw := range []string{
		`{"tasks":[]}`,
		`{"tasks":[{"goal":""}]}`,
		`{"tasks":[{"goal":"a"},{"goal":"b"},{"goal":"c"},{"goal":"d"},{"goal":"e"}]}`,
		`{"tasks":[{"goal":"a","output_schema":{"type":"array"}}]}`,
	} {
		if _, _, err := parseDelegatedTasks(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	prompt := delegatedPrompt(tasks[0])
	for _, want := range []string{"Find Acme's pricing", "Use acme.com", "notebook_write", `"price"`, "misty_finish_task"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt)
		}
	}
}

func TestDelegationResultParsesStructuredAnswersAndCountsRunningChildren(t *testing.T) {
	raw := delegationResult([]db.DelegationChild{
		{TaskIndex: 0, Goal: "Acme", State: "completed", Answer: "Here it is: {\"price\": 12}", OutputSchema: json.RawMessage(`{"type":"object"}`)},
		{TaskIndex: 1, Goal: "Globex", State: "running"},
		{TaskIndex: 2, Goal: "Initech", State: "failed", Error: "task_blocked", Answer: "Blocked by a sign-in."},
	}, 1)
	var result struct {
		Subagents    []map[string]any `json:"subagents"`
		StillRunning int              `json:"still_running"`
		Next         string           `json:"next"`
		ContentTrust string           `json:"contentTrust"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.StillRunning != 1 || !strings.Contains(result.Next, "agents_wait") || result.ContentTrust != "untrusted-subagent-output" {
		t.Fatal(string(raw))
	}
	if output, _ := result.Subagents[0]["output"].(map[string]any); output["price"] != float64(12) {
		t.Fatal("structured answer not parsed", result.Subagents[0])
	}
	if result.Subagents[2]["error"] != "task_blocked" || result.Subagents[2]["answer"] != "Blocked by a sign-in." {
		t.Fatal(result.Subagents[2])
	}
}

func TestNotebookKeysFollowTheParentAndRenderAsMarkdown(t *testing.T) {
	record := &db.AIInvocationRecord{ID: "invocation_1", ConversationID: "conv_1"}
	if notebookKey(record, aiInvocationInput{}) != "conversation:conv_1" {
		t.Fatal("a conversation run should use its conversation's notebook")
	}
	if notebookKey(&db.AIInvocationRecord{ID: "invocation_2"}, aiInvocationInput{}) != "invocation:invocation_2" {
		t.Fatal("a run without a conversation should use its own notebook")
	}
	if notebookKey(&db.AIInvocationRecord{ID: "child"}, aiInvocationInput{NotebookKey: "conversation:conv_1"}) != "conversation:conv_1" {
		t.Fatal("a sub-agent should write to its parent's notebook")
	}
	markdown := notebookMarkdown(&db.AgentNotebook{Plan: "- [x] one", Findings: "Fact (https://a)"})
	if markdown != "Fact (https://a)\n" {
		t.Fatalf("%q", markdown)
	}
}

func TestSubagentsCannotDelegateAndGuidanceMentionsNotebook(t *testing.T) {
	s := &SpacesService{}
	record := &db.AIInvocationRecord{ID: "invocation_1"}
	if got := s.delegationToolRegistrations(record, aiInvocationInput{DelegatedFrom: "invocation_0"}); len(got) != 0 {
		t.Fatal("a sub-agent was offered agents_delegate")
	}
	if got := s.delegationToolRegistrations(record, aiInvocationInput{}); len(got) != 2 {
		t.Fatal("a top-level run should get agents_delegate and agents_wait")
	}
	guidance := agentCapabilityGuidance([]string{"notebook.write", "notebook.read", "agents.delegate"}, false)
	if !strings.Contains(guidance, "notebook_write") || !strings.Contains(guidance, "agents_delegate") {
		t.Fatal(guidance)
	}
}
