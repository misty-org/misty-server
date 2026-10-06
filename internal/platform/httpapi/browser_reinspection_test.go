package api

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestBrowserUnavailableMCPErrorPreservesPublicReason(t *testing.T) {
	for _, code := range []string{"browser_webview_unavailable", "desktop_accessibility_required", "desktop_screen_recording_required"} {
		t.Run(code, func(t *testing.T) {
			result := mcpToolError(browserDeviceFailure(code))
			if !result.IsError || len(result.Content) != 1 || !strings.Contains(fmt.Sprint(result.Content[0]), code) {
				t.Fatalf("native failure category lost: %#v", result)
			}
		})
	}
}

func TestBrowserStaleMCPResultIsExplicitlyNotAttempted(t *testing.T) {
	err := browserDeviceFailure("browser_snapshot_stale")
	if !errors.Is(err, db.ErrAgentToolboxNotAttempted) || !errors.Is(err, errBrowserSnapshotStale) {
		t.Fatalf("native pre-dispatch evidence lost before journaling: %v", err)
	}
	result := mcpToolError(err)
	value, ok := result.StructuredContent.(map[string]any)
	if !ok || result.IsError || value["status"] != "failure" || value["attempted"] != false || value["reason"] != "browser_snapshot_stale" {
		t.Fatalf("missing pre-dispatch rejection: %#v", result)
	}
	unknown := mcpToolError(errors.Join(errBrowserSnapshotStale, db.ErrAgentToolboxActionUnknown))
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

func TestWorkspaceStaleMCPResultRequiresFreshWorkspaceCapture(t *testing.T) {
	err := browserDeviceFailureForOperation("browser_snapshot_stale", "browser.workspace.interact")
	if !errors.Is(err, db.ErrAgentToolboxNotAttempted) || !errors.Is(err, errBrowserSnapshotStale) {
		t.Fatal("workspace pre-dispatch evidence lost")
	}
	result := mcpToolError(err)
	value, ok := result.StructuredContent.(map[string]any)
	if !ok || result.IsError || value["attempted"] != false || value["reason"] != "browser_snapshot_stale" {
		t.Fatalf("missing recoverable workspace rejection: %#v", result)
	}
	message, _ := value["message"].(string)
	if !strings.Contains(message, "browser_workspace_visual") || strings.Contains(message, "browser_inspect") {
		t.Fatalf("wrong observation surface: %s", message)
	}
	if mcpToolError(errors.Join(err, db.ErrAgentToolboxActionUnknown)).StructuredContent.(map[string]any)["status"] != "uncertain" {
		t.Fatal("uncertain input must never be retried as pre-dispatch")
	}
}
