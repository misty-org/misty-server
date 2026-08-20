package unit

import (
	"strings"
	"testing"
	"time"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAIInvocationValidationRejectsOversizedAndUnanchoredSelection(t *testing.T) {
	if err := api.TestingValidateAISelection("selected", "hash"); err != nil {
		t.Fatalf("valid selection rejected: %v", err)
	}
	if err := api.TestingValidateAISelection("selected", ""); err == nil {
		t.Fatal("selection without a content hash was accepted")
	}
	if err := api.TestingValidateAISelection(strings.Repeat("x", (32<<10)+1), "hash"); err == nil {
		t.Fatal("oversized selection was accepted")
	}
}

func TestRecurringBriefingScheduleUsesExplicitTimezoneAndCadence(t *testing.T) {
	after := time.Date(2026, time.March, 7, 17, 0, 0, 0, time.UTC)
	next, err := db.NextAIRecapAt("daily", "08:00", 1, "America/Los_Angeles", after)
	if err != nil || next.Format(time.RFC3339) != "2026-03-08T15:00:00Z" {
		t.Fatalf("daily recap did not preserve local wall time across DST: %v %s", err, next)
	}
	weekly, err := db.NextAIRecapAt("weekly", "09:30", int(time.Monday), "UTC", after)
	if err != nil || weekly.Weekday() != time.Monday || weekly.Hour() != 9 || weekly.Minute() != 30 {
		t.Fatalf("weekly recap was scheduled incorrectly: %v %s", err, weekly)
	}
}

func TestAIInvocationValidationRejectsRawLocalPaths(t *testing.T) {
	if err := api.TestingValidateAIDeviceContext("files-pane", "files-opaque-hash", map[string]any{"selected_count": 2}); err != nil {
		t.Fatalf("opaque device scope rejected: %v", err)
	}
	for _, value := range []string{"/Users/member/secret.txt", `C:\\Users\\member\\secret.txt`, "file:///tmp/secret"} {
		if err := api.TestingValidateAIDeviceContext("files-pane", value, nil); err == nil {
			t.Fatalf("raw local path accepted as opaque scope: %q", value)
		}
	}
	if err := api.TestingValidateAIDeviceContext("files-pane", "files-opaque-hash", map[string]any{"path": "/home/member/private"}); err == nil {
		t.Fatal("raw local path accepted in context metadata")
	}
}

func TestAIInvocationJournalIsIdempotentAndOwnerScoped(t *testing.T) {
	if !api.TestingAIInvocationJournalIsolation() {
		t.Fatal("invocation journal duplicated work or crossed an owner boundary")
	}
}

func TestAccountRetrievalHelpersRankAndBoundUntrustedContent(t *testing.T) {
	if api.TestingAISearchScore("launch blockers", "Launch plan with two blockers") <= api.TestingAISearchScore("launch blockers", "Unrelated launch note") {
		t.Fatal("matching more query terms should rank higher")
	}
	content := strings.Repeat("prefix ", 900) + "needle nearby evidence" + strings.Repeat(" suffix", 900)
	chunk := api.TestingAIRelevantChunk(content, "needle")
	if !strings.Contains(chunk, "needle") || len([]rune(chunk)) > 3202 {
		t.Fatalf("relevant chunk was not bounded: %d runes", len([]rune(chunk)))
	}
}

func TestMistyCitationsOnlyIncludeReferencedSources(t *testing.T) {
	ids := api.TestingMistyCitationIDs("The answer uses [2].", []string{"one", "two"})
	if len(ids) != 1 || ids[0] != "two" {
		t.Fatalf("unexpected citations: %#v", ids)
	}
}

func TestStructuredArtifactContracts(t *testing.T) {
	tasks, err := api.TestingParseAITaskDraftSummaries(`{"tasks":[{"title":" Ship launch ","priority":"HIGH"},{"title":"ship launch","priority":"low"},{"title":"Review metrics","priority":"unexpected"}]}`)
	if err != nil || len(tasks) != 2 || !strings.HasSuffix(tasks[0], ":high") || !strings.HasSuffix(tasks[1], ":medium") || !strings.HasPrefix(tasks[0], "task_") {
		t.Fatalf("unexpected task drafts: %#v %v", tasks, err)
	}
	for _, kind := range []string{"file_plan", "terminal_command", "browser_action", "extension_action"} {
		risk, policy := api.TestingAIArtifactPolicy(kind)
		if risk != "dangerous" || policy != "always_confirm" {
			t.Fatalf("%s can bypass dangerous confirmation", kind)
		}
	}
	summary, operations, err := api.TestingParseAIStructuredArtifact(`{"summary":" Review this ","operations":{"commands":[{"command":"pwd"}]}}`)
	if err != nil || summary != "Review this" || operations["commands"] == nil {
		t.Fatalf("valid artifact rejected: %q %#v %v", summary, operations, err)
	}
	if _, _, err := api.TestingParseAIStructuredArtifact(`{"summary":"x","operations":{},"surprise":true}`); err == nil {
		t.Fatal("unknown artifact envelope field was accepted")
	}
}

func TestAgentArtifactContextSelectsOnlyTheRequestedAuthorizedOutput(t *testing.T) {
	title, content, id := api.TestingAIAgentArtifactText(
		`[{"id":"artifact-one","display_name":"First","text":"private first result"},{"id":"artifact-two","display_name":"Second","summary":"selected result"}]`,
		`{"text":"fallback result"}`,
		"artifact-two",
	)
	if id != "artifact-two" || title != "Second" || !strings.Contains(content, "selected result") || strings.Contains(content, "private first") {
		t.Fatalf("unexpected selected artifact: %q %q %q", id, title, content)
	}
	_, fallback, id := api.TestingAIAgentArtifactText(`[]`, `{"text":"run result"}`, "")
	if id != "run_testing" || fallback != "run result" {
		t.Fatalf("run result fallback was not resolved: %q %q", id, fallback)
	}
}
