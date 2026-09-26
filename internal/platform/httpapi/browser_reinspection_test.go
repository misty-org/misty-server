package api

import (
	"errors"
	"testing"

	"github.com/kannachi323/misty/server/internal/browseractions"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestBrowserStaleMCPResultIsExplicitlyNotAttempted(t *testing.T) {
	err := browserDeviceFailure("browser_snapshot_stale")
	if !errors.Is(err, db.ErrAgentToolboxNotAttempted) || !errors.Is(err, browseractions.ErrStale) {
		t.Fatalf("native pre-dispatch evidence lost before journaling: %v", err)
	}
	result := mcpToolError(err)
	value, ok := result.StructuredContent.(map[string]any)
	if !ok || result.IsError || value["status"] != "failure" || value["attempted"] != false || value["reason"] != "browser_snapshot_stale" {
		t.Fatalf("missing pre-dispatch rejection: %#v", result)
	}
	unknown := mcpToolError(errors.Join(browseractions.ErrStale, db.ErrAgentToolboxActionUnknown))
	if unknown.StructuredContent.(map[string]any)["status"] != "uncertain" {
		t.Fatal("uncertainty must take precedence over a stale reference")
	}
	if !mcpToolError(errors.New("unrelated failure")).IsError {
		t.Fatal("other failures must not become recoverable")
	}
	for _, code := range []string{"device_unavailable", "browser_tab_closed", "script_failed", ""} {
		if errors.Is(browserDeviceFailure(code), db.ErrAgentToolboxNotAttempted) {
			t.Fatalf("unconfirmed failure %q must not become safe to retry", code)
		}
	}
}
