package api

import (
	"encoding/json"
	"github.com/kannachi323/misty/server/internal/capabilities"
	"testing"
)

func TestBrowserRegistryInteractionContract(t *testing.T) {
	var found bool
	for _, tool := range browserToolDescriptors() {
		if tool.Name != "browser.interact" {
			continue
		}
		found = true
		schema, err := capabilities.CompileSchema(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var valid any
		_ = json.Unmarshal([]byte(`{"scopeId":"scope-browser","documentId":"2d9dd46a-90b5-40b3-a72b-b7fb87c57c0d","action":{"kind":"scroll","x":0,"y":400}}`), &valid)
		if err := schema.Validate(valid); err != nil {
			t.Fatal(err)
		}
		delete(valid.(map[string]any), "documentId")
		if schema.Validate(valid) == nil {
			t.Fatal("missing fresh document accepted")
		}
		if companionToolImpact(tool.Name) != "dangerous" || !companionToolNeedsApproval("full", companionToolImpact(tool.Name)) {
			t.Fatal("uncharacterized interaction bypasses approval")
		}
	}
	if !found {
		t.Fatal("browser interaction absent from registry")
	}
}

func TestQuickMCPBrowserCatalogRetainsAuthorizedTools(t *testing.T) {
	descriptors := TestingAIInvocationMCPDescriptors("browser.inspect", "browser.click", "browser.interact")
	if len(descriptors) != 3 {
		t.Fatalf("browser tools lost at MCP boundary: %#v", descriptors)
	}
	for _, descriptor := range descriptors {
		if descriptor.Name != "browser.inspect" && descriptor.Approval != "interactive" {
			t.Fatalf("approval missing: %#v", descriptor)
		}
	}
}
