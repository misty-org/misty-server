package api

import (
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"net/http"
	"testing"
)

func TestAppCannotApproveItsOwnRun(t *testing.T) {
	session := db.AppRuntimeSession{AppID: "app", UserID: "u", Scopes: []string{"agents.write", "ai.write", "agents.read"}}
	for _, path := range []string{"/agent-runs/r/approvals/a", "/runs/r/approval", "/api/agent-runs/r/approvals/a", "/v1/runs/r/approval"} {
		if TestingAuthorizeAppRuntimeRequest(session, http.MethodPost, path) {
			t.Fatalf("app may approve %s", path)
		}
	}
	if !TestingAuthorizeAppRuntimeRequest(session, http.MethodGet, "/agent-runs/r") {
		t.Fatal("run read regressed")
	}
}

func TestAppToolScopeUsesSDKPermissions(t *testing.T) {
	for _, test := range []struct{ name, risk, want string }{
		{"notes.read", "read", "notes.read"}, {"tasks.create", "write", "tasks.write"},
		{"browser.interact", "write", "browser.interact"}, {"members.list", "read", "spaces.read"},
	} {
		if got := appScopeForTool(agenttools.Descriptor{Name: test.name, Risk: test.risk}); got != test.want {
			t.Fatalf("%s: got %s", test.name, got)
		}
	}
	if appScopeForTool(agenttools.Descriptor{Name: "thirdparty.send", Risk: "write"}) != "" {
		t.Fatal("unknown capability gained a scope")
	}
}
