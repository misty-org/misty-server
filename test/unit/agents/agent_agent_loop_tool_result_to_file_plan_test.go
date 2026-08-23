package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/agents"
)

func TestAgentLoopToolResultToFilePlan(t *testing.T) {
	service := NewService(NewSessionStore(0), MockProvider{})
	session := service.CreateSession("user-1")

	err := service.SendMessage(session.ID, "user-1", AgentMessageRequest{
		Mode:        ModeAuto,
		UserMessage: "Organize this desktop folder",
		ActiveRoot:  "Desktop",
		Capabilities: ToolManifest{Tools: []ToolDefinition{
			{Name: ToolListDirectory, Risk: RiskRead},
		}},
	})
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}

	events, err := service.Events(session.ID, "user-1", 0)
	if err != nil {
		t.Fatalf("Events() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("initial events len = %d, want 2: %#v", len(events), events)
	}
	if events[1].Type != EventToolRequest || len(events[1].ToolRequests) != 1 {
		t.Fatalf("expected tool request event, got %#v", events[1])
	}
	if events[1].ToolRequests[0].ApprovalRequired {
		t.Fatalf("auto read tool should not require approval: %#v", events[1].ToolRequests[0])
	}

	result, _ := json.Marshal(map[string]any{
		"entries": []map[string]any{
			{"name": "invoice.pdf"},
			{"name": "photo.png"},
			{"name": "archive.zip"},
		},
	})
	if err := service.SubmitToolResults(session.ID, "user-1", []ToolResult{{
		RequestID: events[1].ToolRequests[0].ID,
		Name:      ToolListDirectory,
		OK:        true,
		Result:    result,
	}}); err != nil {
		t.Fatalf("SubmitToolResults() error = %v", err)
	}

	events, err = service.Events(session.ID, "user-1", 2)
	if err != nil {
		t.Fatalf("Events(after) error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("follow-up events len = %d, want 2: %#v", len(events), events)
	}
	if events[1].Type != EventFilePlan || events[1].FilePlan == nil {
		t.Fatalf("expected file plan event, got %#v", events[1])
	}
	if len(events[1].FilePlan.Operations) < 4 {
		t.Fatalf("file plan operations = %#v, want mkdirs and moves", events[1].FilePlan.Operations)
	}
}

func TestSendMessageRejectsLocalPaths(t *testing.T) {
	service := NewService(NewSessionStore(0), MockProvider{})

	for _, test := range []struct {
		name    string
		request AgentMessageRequest
	}{
		{name: "absolute active root", request: AgentMessageRequest{UserMessage: "help", ActiveRoot: "/Users/misty/Documents"}},
		{name: "windows active root", request: AgentMessageRequest{UserMessage: "help", ActiveRoot: `C:\\Users\\misty\\Documents`}},
		{name: "traversing selected path", request: AgentMessageRequest{UserMessage: "help", ActiveRoot: "scope_abc", SelectedPaths: []string{"../secret.pdf"}}},
		{name: "absolute selected path", request: AgentMessageRequest{UserMessage: "help", ActiveRoot: "scope_abc", SelectedPaths: []string{"/tmp/secret.pdf"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := service.CreateSession("user-1")
			if err := service.SendMessage(session.ID, "user-1", test.request); err == nil {
				t.Fatal("SendMessage() error = nil, want path privacy validation error")
			}
		})
	}
}

func TestSendMessageValidatesSpaceSection(t *testing.T) {
	service := NewService(NewSessionStore(0), MockProvider{})

	for _, test := range []struct {
		name    string
		section string
		wantErr bool
	}{
		{name: "known surface", section: "library"},
		{name: "chat surface", section: "chat"},
		{name: "absent is allowed", section: ""},
		{name: "whitespace is treated as absent", section: "   "},
		{name: "unknown surface", section: "studio", wantErr: true},
		{name: "not a surface at all", section: "../etc/passwd", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := service.CreateSession("user-1")
			err := service.SendMessage(session.ID, "user-1", AgentMessageRequest{
				UserMessage:  "what is open?",
				SpaceSection: test.section,
			})
			if test.wantErr && err == nil {
				t.Fatalf("SendMessage(section=%q) error = nil, want validation error", test.section)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("SendMessage(section=%q) error = %v", test.section, err)
			}
		})
	}
}

// The guard on MaxProviderRequestBytes and the credit reservation both read
// requestSizeBytes, so Space context has to be counted or large contexts bypass
// the friendly error and are under-billed.
func TestRequestSizeBytesCountsPromptAndSpaceContext(t *testing.T) {
	base := TestingRequestSizeBytes(ModelRequest{})
	withPrompt := TestingRequestSizeBytes(ModelRequest{SystemPrompt: strings.Repeat("p", 100)})
	withCard := TestingRequestSizeBytes(ModelRequest{SpaceCard: strings.Repeat("c", 100)})
	withRecords := TestingRequestSizeBytes(ModelRequest{SpaceRecords: strings.Repeat("r", 100)})

	for name, got := range map[string]int{
		"system prompt": withPrompt,
		"space card":    withCard,
		"space records": withRecords,
	} {
		if got != base+100 {
			t.Fatalf("requestSizeBytes() ignored the %s: got %d, want %d", name, got, base+100)
		}
	}
}

func TestSendMessageAcceptsOpaqueScopeAndRelativeSelection(t *testing.T) {
	service := NewService(NewSessionStore(0), MockProvider{})
	session := service.CreateSession("user-1")
	if err := service.SendMessage(session.ID, "user-1", AgentMessageRequest{
		UserMessage:   "summarize this",
		ActiveRoot:    "scope_abc123",
		SelectedPaths: []string{"reports/q2.pdf"},
	}); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
}

func TestAppendExternalAgentMessageReturnsDelegatedRunToAgentSession(t *testing.T) {
	service := NewService(NewSessionStore(0), MockProvider{})
	session := service.CreateSession("user-1")
	event, err := service.AppendExternalAgentMessage(context.Background(), session.ID, "user-1", "run-1", "Delegated work finished")
	if err != nil {
		t.Fatalf("AppendExternalAgentMessage() error = %v", err)
	}
	if event.Type != EventAgentMessage || event.RunID != "run-1" || event.Text != "Delegated work finished" || event.Sequence != 1 {
		t.Fatalf("external event = %#v", event)
	}
	events, err := service.Events(session.ID, "user-1", 0)
	if err != nil || len(events) != 1 || events[0].Sequence != event.Sequence || events[0].RunID != event.RunID || events[0].Text != event.Text {
		t.Fatalf("Events() = %#v, %v", events, err)
	}
	if _, err := service.AppendExternalAgentMessage(context.Background(), session.ID, "other-user", "run-1", "secret"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("cross-user append error = %v", err)
	}
}

type loopingToolProvider struct{ calls int }

func (provider *loopingToolProvider) Next(ModelRequest) (ModelResponse, error) {
	provider.calls++
	return ModelResponse{ToolRequests: []ToolRequest{{
		ID: fmt.Sprintf("tool-%d", provider.calls), Name: ToolListDirectory, Risk: RiskRead,
	}}}, nil
}

func TestProviderCallsAreCappedPerUserTurn(t *testing.T) {
	provider := &loopingToolProvider{}
	service := NewService(NewSessionStore(0), provider)
	session := service.CreateSession("user")
	if err := service.SendMessage(session.ID, "user", AgentMessageRequest{Mode: ModeAuto, UserMessage: "loop", Capabilities: ToolManifest{Tools: []ToolDefinition{{Name: ToolListDirectory, Risk: RiskRead}}}}); err != nil {
		t.Fatal(err)
	}
	for expectedCall := 2; expectedCall <= MaxProviderCallsPerTurn; expectedCall++ {
		events, err := service.Events(session.ID, "user", 0)
		if err != nil {
			t.Fatal(err)
		}
		var pending *ToolRequest
		for index := range events {
			if len(events[index].ToolRequests) > 0 {
				candidate := events[index].ToolRequests[0]
				pending = &candidate
			}
		}
		if pending == nil {
			t.Fatalf("missing pending tool request before call %d: %#v", expectedCall, events)
		}
		if err := service.SubmitToolResults(session.ID, "user", []ToolResult{{RequestID: pending.ID, Name: pending.Name, OK: true}}); err != nil {
			t.Fatal(err)
		}
	}
	if provider.calls != MaxProviderCallsPerTurn {
		t.Fatalf("provider calls = %d, want hard cap %d", provider.calls, MaxProviderCallsPerTurn)
	}
	events, err := service.Events(session.ID, "user", 0)
	if err != nil {
		t.Fatal(err)
	}
	if last := events[len(events)-1]; last.Type != EventError || !strings.Contains(last.Message, "tool step limit") {
		t.Fatalf("last event = %#v", last)
	}
}

type serverToolProvider struct {
	calls         int
	systemPrompts []string
}

func (provider *serverToolProvider) Next(request ModelRequest) (ModelResponse, error) {
	provider.calls++
	provider.systemPrompts = append(provider.systemPrompts, request.SystemPrompt)
	if provider.calls == 1 {
		return ModelResponse{ToolRequests: []ToolRequest{{ID: "read-1", Name: "workflow.read_content", Risk: RiskRead, Arguments: json.RawMessage(`{"resourceId":"doc-1"}`)}}}, nil
	}
	if len(request.ToolResults) != 1 || !request.ToolResults[0].OK {
		return ModelResponse{}, errors.New("missing tool result")
	}
	return ModelResponse{Text: "Grounded answer"}, nil
}

func TestCompleteWithToolsUsesTheValidatedAgentToolLoop(t *testing.T) {
	provider := &serverToolProvider{}
	service := NewService(NewSessionStore(0), provider)
	completion, err := service.CompleteWithToolsContext(context.Background(), "user", "user", "You are Scout. Be terse.", "Read the document", TierLow, ToolManifest{Tools: []ToolDefinition{{Name: "workflow.read_content", Risk: RiskRead}}}, func(_ context.Context, request ToolRequest) (json.RawMessage, error) {
		if request.Name != "workflow.read_content" {
			t.Fatalf("tool = %#v", request)
		}
		return json.RawMessage(`{"sections":[{"text":"evidence"}]}`), nil
	})
	if err != nil || completion.Text != "Grounded answer" || completion.ToolCalls != 1 || provider.calls != 2 {
		t.Fatalf("completion=%#v calls=%d err=%v", completion, provider.calls, err)
	}
	// The Agent identity has to reach the provider as the system prompt on every
	// round: the prompt builder publishes it as agent_instructions_and_context,
	// and an Agent whose instructions only appear in the user message is refused
	// as personaless.
	for round, systemPrompt := range provider.systemPrompts {
		if systemPrompt != "You are Scout. Be terse." {
			t.Fatalf("system prompt on round %d = %q", round+1, systemPrompt)
		}
	}
}
