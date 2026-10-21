package api

import (
	"encoding/json"
	"strings"
	"testing"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func openedTab(device, ref string) aiInvocationDeviceContext {
	return aiInvocationDeviceContext{
		DeviceID: device, Kind: "browser_tab", OpaqueRef: ref,
		Capabilities: json.RawMessage(`["browser.snapshot","browser.steps"]`),
		Metadata:     json.RawMessage(`{"app_id":"browser","remote_session":true,"task_id":"hosted"}`),
		RunGrant:     &signedDeviceRecord{Payload: "p", Signature: "s"},
	}
}

func TestDeviceOpenedTabsMustBeTheDevicesOwnBrowserTabs(t *testing.T) {
	if err := validateDeviceOpenedTabs([]aiInvocationDeviceContext{openedTab("device-b", "scope-1")}, "device-b", 1); err != nil {
		t.Fatalf("a valid tab was refused: %v", err)
	}
	cases := map[string]func(*aiInvocationDeviceContext){
		"another device":      func(tab *aiInvocationDeviceContext) { tab.DeviceID = "device-c" },
		"not a browser tab":   func(tab *aiInvocationDeviceContext) { tab.Kind = "local_folder" },
		"no grant":            func(tab *aiInvocationDeviceContext) { tab.RunGrant = nil },
		"a folder capability": func(tab *aiInvocationDeviceContext) { tab.Capabilities = json.RawMessage(`["folder.read"]`) },
		"no browser app":      func(tab *aiInvocationDeviceContext) { tab.Metadata = json.RawMessage(`{}`) },
		"an empty scope":      func(tab *aiInvocationDeviceContext) { tab.OpaqueRef = " " },
		"a terminal ability":  func(tab *aiInvocationDeviceContext) { tab.Capabilities = json.RawMessage(`["terminal.execute"]`) },
	}
	for name, change := range cases {
		tab := openedTab("device-b", "scope-1")
		change(&tab)
		if validateDeviceOpenedTabs([]aiInvocationDeviceContext{tab}, "device-b", 1) == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if validateDeviceOpenedTabs([]aiInvocationDeviceContext{openedTab("device-b", "a"), openedTab("device-b", "a")}, "device-b", 2) == nil {
		t.Error("the same scope twice was accepted")
	}
	if validateDeviceOpenedTabs([]aiInvocationDeviceContext{openedTab("device-b", "a")}, "device-b", 2) == nil {
		t.Error("fewer tabs than asked for were accepted")
	}
}

func TestAnotherDevicesSessionRunsUnderItsOwnTask(t *testing.T) {
	body := aiInvocationInput{TaskID: "laptop-task", DeviceContexts: []aiInvocationDeviceContext{openedTab("device-b", "remote-scope")}}
	if task := scopeTaskID(body, "remote-scope"); task != "hosted" {
		t.Fatalf("remote scope task = %q", task)
	}
	if task := scopeTaskID(body, "local-scope"); task != "laptop-task" {
		t.Fatalf("local scope task = %q", task)
	}
}

func TestOnlyTheSendingComputersBrowserGrantOpensASession(t *testing.T) {
	own := db.AIInvocationContext{Kind: "workspace", State: "attached", DeviceID: "laptop", Capabilities: json.RawMessage(`["browser.session"]`), Metadata: json.RawMessage(`{"source":"desktop"}`)}
	other := db.AIInvocationContext{Kind: "workspace", State: "attached", DeviceID: "mac-mini", Capabilities: json.RawMessage(`["browser.session"]`), Metadata: json.RawMessage(`{"source":"device"}`)}
	detached := own
	detached.State = "detached"
	if target := sessionTarget([]db.AIInvocationContext{other, own}); target == nil || target.DeviceID != "laptop" {
		t.Fatalf("target = %#v", target)
	}
	if sessionTarget([]db.AIInvocationContext{other}) != nil {
		t.Fatal("another device's grant opened a session")
	}
	if sessionTarget([]db.AIInvocationContext{detached}) != nil {
		t.Fatal("a detached grant opened a session")
	}
}

func TestAnOfflineDeviceEndsTheRunWithWordsThePersonCanActOn(t *testing.T) {
	err := deviceOfflineError{name: "Mac mini", reason: "went offline."}
	result := mcpToolError(err)
	text := result.Content[0].(interface{ MarshalJSON() ([]byte, error) })
	raw, _ := text.MarshalJSON()
	if !result.IsError || !strings.Contains(string(raw), "device_offline: Mac mini went offline. Ask again once Misty is open there.") {
		t.Fatalf("tool error = %s", raw)
	}
}
