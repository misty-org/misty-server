package agent

import (
	"strings"
	"context"
	"testing"
)

type readOnlyInstructionProvider struct {
	request ModelRequest
}

func (p *readOnlyInstructionProvider) Next(request ModelRequest) (ModelResponse, error) {
	p.request = request
	return ModelResponse{Text: `{"route":"steer"}`}, nil
}

func TestPlainCompletionKeepsInstructionsEmpty(t *testing.T) {
	provider := &readOnlyInstructionProvider{}
	service := NewService(nil, provider)
	if _, _, err := service.CompleteWithTierContext(context.Background(), "member", "hello", "automation_ai", TierLow); err != nil {
		t.Fatal(err)
	}
	if provider.request.SystemPrompt != "" || provider.request.Messages[0].Content != "hello" {
		t.Fatalf("plain request = %#v", provider.request)
	}
}

func TestReadOnlyCompletionRetainsAuthoritativeInstructions(t *testing.T) {
	provider := &readOnlyInstructionProvider{}
	service := NewService(nil, provider)
	instructions := "You are the read-only follow-up router."
	message := `{"new_message":"Stop before publishing"}`
	result, err := service.CompleteWithToolsForSpaceContext(context.Background(), "member", "owner", "space", instructions, message, TierLow, ToolManifest{}, nil)
	if err != nil || result.Text != `{"route":"steer"}` || result.ToolCalls != 0 {
		t.Fatalf("completion = %#v, error = %v", result, err)
	}
	request := provider.request
	if request.SystemPrompt != instructions || len(request.Messages) != 1 || request.Messages[0].Content != message {
		t.Fatalf("instructions and user data were not separated: %#v", request)
	}
	if request.UserID != "member" || request.Mode != ModeAsk || len(request.Capabilities.Tools) != 0 {
		t.Fatalf("read-only requesting-account scope changed: %#v", request)
	}
	if !strings.Contains(promptText(request), `"agent_instructions_and_context": "`+instructions+`"`) {
		t.Fatal("instructions missing from the canonical provider prompt")
	}
}

func promptText(request ModelRequest) string {
	prompt, _ := buildAgentPromptWithImages(request)
	return prompt
}
