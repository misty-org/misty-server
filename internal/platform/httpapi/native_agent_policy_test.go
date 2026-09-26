package api

import (
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"testing"
)

func TestNativeAgentWorkspaceToolFamilies(t *testing.T) {
	builtins := map[string]bool{"browser": true, "files": true}
	for _, mode := range []string{"user", "agent", "team"} {
		for _, name := range []string{"browser.inspect", "browser.upload", "files.list", "agents.configure", "memory.remember", "mcp.call", "weather.current", "spaces.list", "spaces.tools", "spaces.execute"} {
			if !nativeAgentToolAllowed(name, serveragent.RiskWrite, mode, builtins) {
				t.Errorf("built-in %s unavailable in %s", name, mode)
			}
		}
		// Historical installations cannot re-enable retired surfaces.
		legacy := map[string]bool{"planner": true, "journal": true, "chat": true, "library": true, "terminal": true, "code": true, "inbox": true}
		for _, name := range []string{"context.get", "tasks.create", "notes.get", "messages.list", "library.list", "terminal.exec", "code.edit", "mail.send", "space.get", "members.list", "ask.delegate", "unknown.execute", "browser.upload", "files.list"} {
			if nativeAgentToolAllowed(name, serveragent.RiskWrite, mode, legacy) {
				t.Errorf("retired/unavailable %s admitted in %s", name, mode)
			}
		}
		if got := nativeAgentToolAllowed("browser.workspace.interact", serveragent.RiskWrite, mode, builtins); got != (mode == "agent") {
			t.Errorf("foreground authority for %s: %v", mode, got)
		}
	}
}

func TestNativeAgentContextUsesBrowserWorkspace(t *testing.T) {
	refs := []aiContextReference{
		{Kind: "space", ID: "history", Metadata: map[string]any{"unscoped": "content"}},
		{Kind: "note", ID: "journal"}, {Kind: "task", ID: "planner"},
		{Kind: "workspace.scope", ID: "workspace"},
		{Kind: "browser-tab", ID: "old-provider", Metadata: map[string]any{"app_id": "planner"}},
		{Kind: "browser-tab", ID: "browser", Metadata: map[string]any{"app_id": "browser"}},
		{Kind: "browser-tab", ID: "missing-metadata"},
	}
	filtered := filterNativeAgentContext(refs, []string{"browser", "files"})
	if len(filtered) != 5 || filtered[0].ID != "history" || filtered[4].ID != "browser" {
		t.Fatalf("unexpected context: %+v", filtered)
	}
	filtered = filterNativeAgentContext(refs, []string{"planner", "journal"})
	if len(filtered) != 4 || filtered[0].ID != "history" {
		t.Fatalf("legacy installations revived context: %+v", filtered)
	}
}
