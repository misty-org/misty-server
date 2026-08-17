package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestBrowserToolDescriptorsAreDeviceLocalAndClosed(t *testing.T) {
	descriptors := browserToolDescriptors()
	if len(descriptors) != 4 {
		t.Fatalf("browserToolDescriptors() returned %d tools, want 4", len(descriptors))
	}
	want := map[string]bool{
		"browser.inspect":        true,
		"browser.navigate":       true,
		"browser.click":          true,
		"browser.downloads.list": true,
	}
	for _, descriptor := range descriptors {
		if !want[descriptor.Name] {
			t.Fatalf("unexpected browser tool %q", descriptor.Name)
		}
		if descriptor.Locality != agenttools.LocalityDevice || descriptor.Approval != agenttools.ApprovalNone {
			t.Fatalf("%s locality/approval = %q/%q", descriptor.Name, descriptor.Locality, descriptor.Approval)
		}
		delete(want, descriptor.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing browser descriptors: %#v", want)
	}
}

func TestActiveBrowserGrantTabsRejectExpiredRevokedAndNonBrowserGrants(t *testing.T) {
	now := time.Now()
	activeMetadata := json.RawMessage(`{"kind":"browser_tab","label":"Docs","origin":"https://example.com"}`)
	grants := []db.AgentDeviceGrant{
		{ID: "active", ScopeID: "browser-tab-1", Capabilities: json.RawMessage(`["browser.inspect"]`), Metadata: activeMetadata, ExpiresAt: now.Add(time.Hour)},
		{ID: "expired", ScopeID: "browser-tab-2", Capabilities: json.RawMessage(`["browser.inspect"]`), Metadata: activeMetadata, ExpiresAt: now.Add(-time.Minute)},
		{ID: "files", ScopeID: "files", Capabilities: json.RawMessage(`["files.read"]`), Metadata: json.RawMessage(`{}`), ExpiresAt: now.Add(time.Hour)},
	}
	revokedAt := now
	grants = append(grants, db.AgentDeviceGrant{ID: "revoked", ScopeID: "browser-tab-3", Capabilities: json.RawMessage(`["browser.click"]`), Metadata: activeMetadata, ExpiresAt: now.Add(time.Hour), RevokedAt: &revokedAt})

	tabs := activeBrowserGrantTabs(grants)
	if len(tabs) != 1 || tabs[0] != "Docs (scopeId browser-tab-1)" {
		t.Fatalf("activeBrowserGrantTabs() = %#v", tabs)
	}
}
