package api

import (
	"strings"
	"testing"
)

func TestRunCatalogRetainsGrantedBrowserTools(t *testing.T) {
	toolbox := buildAgentToolbox(nil, agentToolboxOptions{
		accountLevel: true, browserTabs: []string{"Browser (scopeId scope-browser)"},
		browserCapabilities: map[string]bool{"browser.inspect": true, "browser.navigate": true, "browser.act": true},
	})
	granted := 0
	for _, descriptor := range toolbox.Descriptors() {
		if !strings.HasPrefix(descriptor.Name, "browser.") {
			continue
		}
		granted++
		if descriptor.Approval != "none" || !strings.Contains(descriptor.Description, "scope-browser") {
			t.Fatalf("unexpected browser descriptor: %+v", descriptor)
		}
	}
	if granted != 3 {
		t.Fatalf("granted browser tools lost from the catalog: %d", granted)
	}
}
