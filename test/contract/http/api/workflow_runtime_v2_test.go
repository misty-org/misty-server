package api

import (
	"encoding/json"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func TestNormalizeContentPagePaginatesWithStableCitations(t *testing.T) {
	text := strings.Repeat("evidence ", 1200)
	run := &db.SpaceRun{ID: "run_content", SpaceID: "space_content"}
	output, err := TestingNormalizeContentPage(run, workflowv2.Invocation{NodeID: "reader", Config: json.RawMessage(`{"pageSize":1}`), Input: TestingMustAPIRawJSON(map[string]any{
		"contentRef": map[string]any{"sourceKind": "message", "providerId": "slack", "resourceId": "message-1", "fingerprint": "old", "displayName": "Message", "permissionScope": "channel:C1"},
		"text":       text,
	})})
	if err != nil {
		t.Fatal(err)
	}
	var page workflowv2.ContentPage
	if json.Unmarshal(output, &page) != nil || len(page.Sections) != 1 || len(page.Citations) != 1 || page.NextCursor != "1" || !page.Truncated || !page.SourceChanged {
		t.Fatalf("page = %#v", page)
	}
	if page.Citations[0].Content.ResourceID != "message-1" || page.Citations[0].Locator != page.Sections[0].Locator {
		t.Fatalf("citation = %#v", page.Citations[0])
	}
}

func TestNormalizeContentPageRejectsMissingContent(t *testing.T) {
	_, err := TestingNormalizeContentPage(&db.SpaceRun{ID: "run_empty"}, workflowv2.Invocation{NodeID: "reader", Config: json.RawMessage(`{}`), Input: json.RawMessage(`{"contentRef":{"sourceKind":"remote"}}`)})
	if err != workflowv2.ErrUnsupportedContent {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeJSONObjectAcceptsFencedStructuredAgentOutput(t *testing.T) {
	if got := string(TestingDecodeJSONObject("```json\n{\"answer\":\"done\"}\n```")); got != `{"answer":"done"}` {
		t.Fatalf("decodeJSONObject() = %q", got)
	}
}

func TestWorkflowResourceIdentityUsesStableProviderResourceAndFingerprint(t *testing.T) {
	key, fingerprint := TestingWorkflowResourceIdentity(json.RawMessage(`{"destination":".summaries"}`), json.RawMessage(`{"content":{"providerId":"library","resourceId":"item-1","fingerprint":"sha-1"}}`))
	if key != "library:item-1" || fingerprint != "sha-1" {
		t.Fatalf("identity = %q, %q", key, fingerprint)
	}
}

func TestWorkflowEventIdentityAcceptsCanonicalAndProviderEventFields(t *testing.T) {
	provider, eventID := TestingWorkflowEventIdentity(json.RawMessage(`{"provider":"device","eventId":"evt-1"}`))
	if provider != "device" || eventID != "evt-1" {
		t.Fatalf("identity = %q, %q", provider, eventID)
	}
}

func TestWorkflowEventAndContentDiscoveryTraversesTypedPortWrappers(t *testing.T) {
	value := map[string]any{"input": map[string]any{"value": map[string]any{"events": []any{map[string]any{"eventId": "evt-1"}}}}}
	items := TestingFindWorkflowItems(value)
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	content := map[string]any{"input": map[string]any{"item": map[string]any{"contentRef": map[string]any{"resourceId": "item-1"}}}}
	found := TestingFindContentInput(content)
	if found == nil || TestingFindWorkflowString(found, "resourceId") != "item-1" {
		t.Fatalf("content = %#v", found)
	}
}

func TestEvaluateControlBranchEmitsOnlySelectedPort(t *testing.T) {
	output, err := TestingEvaluateControlBranch("condition", workflowv2.Invocation{Config: json.RawMessage(`{"path":"input.total","operator":"gte","value":10}`), Input: json.RawMessage(`{"input":{"total":12}}`)})
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	_ = json.Unmarshal(output, &value)
	if value["matched"] != true || value["true"] == nil || value["false"] != nil {
		t.Fatalf("output = %s", output)
	}
}

func TestWorkflowApprovalEnvelopePreservesExactOutboundContext(t *testing.T) {
	run := &db.SpaceRun{ID: "run-1", AgentID: "agent-1", AgentVersionID: "agent-version-1", WorkflowVersionID: "workflow-version-1"}
	raw := TestingWorkflowApprovalEnvelope(run, "provider.slack.write", "slack", "connection-1", "channel:C123", json.RawMessage(`{"text":"Ship it","reason":"Answer the cited question","citations":[{"locator":"thread:171.2"}]}`))
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if value["provider"] != "slack" || value["destination"] != "channel:C123" || value["content_preview"] != "Ship it" || value["reason"] != "Answer the cited question" {
		t.Fatalf("approval envelope = %s", raw)
	}
	citations, _ := value["citations"].([]any)
	if len(citations) != 1 || value["run_id"] != "run-1" {
		t.Fatalf("approval provenance = %s", raw)
	}
}
