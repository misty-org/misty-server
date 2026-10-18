package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/integrations/composio"
)

func TestAppsToolsRegisterOnlyWhenConfigured(t *testing.T) {
	t.Setenv("MISTY_COMPOSIO_DEPLOYMENT", "")
	t.Setenv("COMPOSIO_API_KEY", "test-key")
	if registrations := (&SpacesService{}).appsToolRegistrations(); len(registrations) != 0 {
		t.Fatalf("apps registered without the Cloud opt-in: %d", len(registrations))
	}
	t.Setenv("MISTY_COMPOSIO_DEPLOYMENT", "cloud")
	registrations := (&SpacesService{}).appsToolRegistrations()
	if _, err := agenttools.New(registrations...); err != nil || len(registrations) != 5 {
		t.Fatalf("app tools = %d, %v", len(registrations), err)
	}
	for _, registration := range registrations {
		descriptor := registration.Descriptor
		if descriptor.Name == appsExecuteTool && descriptor.Risk != "write" || descriptor.Name != appsExecuteTool && descriptor.Risk != "read" {
			t.Errorf("%s risk = %s", descriptor.Name, descriptor.Risk)
		}
	}
}

func TestAppsSearchViewSpeaksMistyToolNames(t *testing.T) {
	var result composio.SearchResult
	if err := json.Unmarshal([]byte(`{"success":true,"results":[{"use_case":"upload to drive","execution_guidance":"Use COMPOSIO_MULTI_EXECUTE_TOOL after COMPOSIO_GET_TOOL_SCHEMAS.","primary_tool_slugs":["GOOGLEDRIVE_CREATE_FILE_FROM_TEXT"],"related_tool_slugs":[],"toolkits":["googledrive"]}],
		"toolkit_connection_statuses":[{"toolkit":"googledrive","has_active_connection":false,"accounts":[]}],
		"tool_schemas":{"GOOGLEDRIVE_CREATE_FILE_FROM_TEXT":{"toolkit":"googledrive","description":"Create a file","input_schema":{"type":"object"},"hasFullSchema":true},"GOOGLEDRIVE_FIND_FOLDER":{"toolkit":"googledrive","hasFullSchema":false}}}`), &result); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(appsSearchView(result, searchAppPolicy{}, runApps{}))
	text := string(encoded)
	for _, want := range []string{"integration_execute after integration_schemas", `"input_schema":{"type":"object"}`, "Call integration_schemas for this tool", "Call integration_connect with app googledrive"} {
		if !strings.Contains(text, want) {
			t.Errorf("search view lacks %q: %s", want, text)
		}
	}
	if strings.Contains(text, "COMPOSIO_") {
		t.Fatalf("Composio meta tool names reached the model: %s", text)
	}
}

func TestAppsApprovalBindsExactArguments(t *testing.T) {
	first := appsCall{ToolSlug: "GMAIL_SEND_EMAIL", Arguments: json.RawMessage(`{"to":"a@example.com","subject":"Launch"}`)}
	reordered := appsCall{ToolSlug: "GMAIL_SEND_EMAIL", Arguments: json.RawMessage(`{"subject":"Launch","to":"a@example.com"}`)}
	if appsArgumentsHash(first) != appsArgumentsHash(reordered) {
		t.Fatal("key order changed the approval identity")
	}
	for _, other := range []appsCall{
		{ToolSlug: "GMAIL_SEND_EMAIL", Arguments: json.RawMessage(`{"to":"b@example.com","subject":"Launch"}`)},
		{ToolSlug: "GMAIL_SEND_EMAIL", Arguments: first.Arguments, Account: "work"},
		{ToolSlug: "GMAIL_CREATE_EMAIL_DRAFT", Arguments: first.Arguments},
	} {
		if appsArgumentsHash(other) == appsArgumentsHash(first) {
			t.Fatalf("approval for %+v would cover a different action", other)
		}
	}
	info := composio.ToolInfo{Slug: "GMAIL_SEND_EMAIL", Name: "Send Email"}
	info.Toolkit.Name = "Gmail"
	title, summary := appsActionSummary(info, appsCall{ToolSlug: "GMAIL_SEND_EMAIL", Arguments: first.Arguments, Account: "work"})
	if title != "Gmail · Send Email" || summary != "subject: Launch\nto: a@example.com\naccount: work" {
		t.Fatalf("approval card = %q / %q", title, summary)
	}
}

func TestAppsBoundedKeepsResultsSmall(t *testing.T) {
	if string(appsBounded(json.RawMessage(`{"ok":true}`))) != `{"ok":true}` || string(appsBounded(nil)) != `{}` {
		t.Fatal("small results changed")
	}
	large := json.RawMessage(`{"items":"` + strings.Repeat("x", appsResultLimit) + `"}`)
	var bounded map[string]any
	if err := json.Unmarshal(appsBounded(large), &bounded); err != nil || bounded["truncated"] != true || len(bounded["preview"].(string)) > appsResultLimit+8 {
		t.Fatalf("large result was not bounded: %v", err)
	}
}

func TestSearchAppsNeedTheUserToNameThem(t *testing.T) {
	quiet := searchAppPolicy{webSearch: true, request: "Research a vegetarian weight-loss guide for an exam"}
	if quiet.mayConnect("exa") || !quiet.mayConnect("googledrive") {
		t.Fatal("an unnamed search app may connect, or an ordinary app may not")
	}
	if next := quiet.next("exa"); !strings.Contains(next, "web_search") || strings.Contains(next, "integration_connect") {
		t.Fatalf("unnamed search app next = %q", next)
	}
	named := searchAppPolicy{webSearch: true, request: "Use Exa to find recent papers"}
	if !named.mayConnect("exa") || !strings.Contains(named.next("exa"), "integration_connect") {
		t.Fatal("a search app the user named cannot connect")
	}
	offered := searchAppPolicy{offer: true, request: "Find recent papers"}
	if !offered.mayConnect("tavily") || !strings.Contains(offered.next("tavily"), "Offer it only if") {
		t.Fatal("Offer search apps does not let the agent offer one")
	}
}
