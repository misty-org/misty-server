package unit

import (
	"encoding/json"
	"testing"

	mcpintegration "github.com/kannachi323/misty/server/internal/integrations/mcp"
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	"golang.org/x/oauth2"
)

func TestActivepiecesToolBoundary(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"ap_create_flow", "ap_build_flow", "ap_lock_and_publish", "ap_list_runs"} {
		if !api.TestingAllowedActivepiecesMCPTool(name) {
			t.Fatalf("expected %s to be available", name)
		}
	}
	for _, name := range []string{"ap_delete_flow", "ap_run_action", "ap_create_table", "unknown_tool"} {
		if api.TestingAllowedActivepiecesMCPTool(name) {
			t.Fatalf("expected %s to be excluded", name)
		}
	}
}

func TestActivepiecesDiscoveryFiltersToolsBeforeStorage(t *testing.T) {
	t.Parallel()
	schema := json.RawMessage(`{"type":"object","properties":{}}`)
	tools := api.TestingNormalizeMCPDiscoveryForProvider("connection", "activepieces", []mcpintegration.Tool{
		{Name: "ap_create_flow", InputSchema: schema},
		{Name: "ap_delete_flow", InputSchema: schema},
	})
	if len(tools) != 1 || tools[0].RemoteName != "ap_create_flow" {
		t.Fatalf("unexpected filtered catalog: %#v", tools)
	}
}

func TestMCPToolSchemaAcceptsBoundedCompositions(t *testing.T) {
	t.Parallel()
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"input":{"anyOf":[{"type":"string"},{"type":"null"}]},
			"values":{"type":"object","additionalProperties":{"type":"string"}}
		}
	}`)
	if err := api.TestingValidateMCPToolSchema(schema); err != nil {
		t.Fatalf("expected composed schema to be accepted: %v", err)
	}
}

func TestMCPToolSchemaRejectsExternalReferences(t *testing.T) {
	t.Parallel()
	schema := json.RawMessage(`{"type":"object","properties":{"input":{"$ref":"https://example.com/schema.json"}}}`)
	if err := api.TestingValidateMCPToolSchema(schema); err == nil {
		t.Fatal("expected external schema reference to be rejected")
	}
}

func TestMCPOAuthAuthStyle(t *testing.T) {
	t.Parallel()
	if got := api.TestingMCPOAuthAuthStyle("client_secret_post"); got != oauth2.AuthStyleInParams {
		t.Fatalf("unexpected post auth style: %v", got)
	}
	if got := api.TestingMCPOAuthAuthStyle("client_secret_basic"); got != oauth2.AuthStyleInHeader {
		t.Fatalf("unexpected basic auth style: %v", got)
	}
}

func TestActivepiecesMCPURLComesFromServerConfiguration(t *testing.T) {
	t.Setenv("MISTY_ACTIVEPIECES_MCP_URL", "https://automations.mistysys.com/mcp")
	got, err := api.TestingConfiguredActivepiecesMCPURL()
	if err != nil || got != "https://automations.mistysys.com/mcp" {
		t.Fatalf("configured endpoint=%q err=%v", got, err)
	}
}

func TestActivepiecesMCPURLRejectsMissingOrInsecureConfiguration(t *testing.T) {
	for _, endpoint := range []string{"", "http://activepieces-app/mcp", "not-a-url"} {
		t.Run(endpoint, func(t *testing.T) {
			t.Setenv("MISTY_ACTIVEPIECES_MCP_URL", endpoint)
			if _, err := api.TestingConfiguredActivepiecesMCPURL(); err == nil {
				t.Fatalf("expected %q to be rejected", endpoint)
			}
		})
	}
}
