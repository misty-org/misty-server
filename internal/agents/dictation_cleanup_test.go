package agent

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/modelruntime"
)

func TestDictationCleanupTreatsSpeechAsDataAndFallsBack(t *testing.T) {
	reply := "Let's move the review to Thursday."
	fake := modelruntime.TestingNewFake(t, func(call modelruntime.TestingCall) (int, any) {
		return http.StatusOK, map[string]any{"text": reply, "usage": map[string]int{"inputTokens": 40, "outputTokens": 9}}
	})
	service := &Service{models: fake.Client}
	got, err := service.CleanDictation(t.Context(), "user-1", "openai/fast", DictationCleanup{Text: "um let's uh move the review to thursday", Style: "loud", Dictionary: []string{"Misty"}})
	if err != nil || got != reply {
		t.Fatalf("CleanDictation() = %q, %v", got, err)
	}
	call := fake.Calls()[0]
	messages, _ := call.Body["messages"].([]any)
	prompt := ""
	if len(messages) == 1 {
		content, _ := messages[0].(map[string]any)["content"].([]any)
		prompt, _ = content[0].(map[string]any)["text"].(string)
	}
	if !strings.Contains(prompt, "Style: plain") || !strings.Contains(prompt, "Dictionary: Misty") || !strings.Contains(prompt, "<dictation>\num let's uh move") {
		t.Fatalf("prompt = %q", prompt)
	}
	if system, _ := call.Body["system"].(string); !strings.Contains(system, "Never add, answer") {
		t.Fatalf("system = %q", system)
	}
	reply = "   "
	if got, _ = service.CleanDictation(t.Context(), "user-1", "openai/fast", DictationCleanup{Text: "keep me"}); got != "keep me" {
		t.Fatalf("empty reply should keep the original, got %q", got)
	}
	if got, _ = service.CleanDictation(t.Context(), "user-1", "openai/fast", DictationCleanup{Text: "  "}); got != "" || len(fake.Calls()) != 2 {
		t.Fatal("blank dictation should not call the model")
	}
}
