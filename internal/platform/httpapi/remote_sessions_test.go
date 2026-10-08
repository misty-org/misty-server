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
		"a folder capability": func(tab *aiInvocationDeviceContext) { tab.Capabilities = json.RawMessage(`["files.read"]`) },
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

func TestOnlyAPickedDevicesBrowserGrantOpensASession(t *testing.T) {
	contexts := []db.AIInvocationContext{
		{Kind: "workspace", State: "attached", DeviceID: "laptop", Capabilities: json.RawMessage(`["tabs.list"]`)},
		{Kind: "workspace", State: "detached", DeviceID: "old", Capabilities: json.RawMessage(`["browser.session"]`)},
		{Kind: "workspace", State: "attached", DeviceID: "mac-mini", Capabilities: json.RawMessage(`["tabs.list","browser.session"]`)},
	}
	if target := remoteSessionTarget(contexts); target == nil || target.DeviceID != "mac-mini" {
		t.Fatalf("target = %#v", target)
	}
	if remoteSessionTarget(contexts[:2]) != nil {
		t.Fatal("a request without a picked device opened a session")
	}
}

func TestAnOfflineDeviceEndsTheRunWithWordsThePersonCanActOn(t *testing.T) {
	err := deviceOfflineError{name: "Mac mini", reason: "went offline."}
	result := mcpToolError(err)
	text := result.Content[0].(interface{ MarshalJSON() ([]byte, error) })
	raw, _ := text.MarshalJSON()
	if !result.IsError || !strings.Contains(string(raw), "device_offline: Mac mini went offline. Pick another device and ask again.") {
		t.Fatalf("tool error = %s", raw)
	}
}

func TestTheAgentIsToldWhichDeviceThePersonPicked(t *testing.T) {
	picked := aiInvocationInput{DeviceContexts: []aiInvocationDeviceContext{{
		Kind: "workspace", DisplayName: "Misty browser on Mac mini", Capabilities: json.RawMessage(`["tabs.list","browser.session"]`),
	}}}
	if guidance := pickedDeviceGuidance(picked); !strings.Contains(guidance, "the person picked Mac mini") || !strings.Contains(guidance, "screen_open opens a browser there") {
		t.Fatalf("guidance = %q", guidance)
	}
	own := aiInvocationInput{DeviceContexts: []aiInvocationDeviceContext{{Kind: "workspace", Capabilities: json.RawMessage(`["tabs.list"]`)}}}
	if guidance := pickedDeviceGuidance(own); guidance != "" {
		t.Fatalf("a request on this computer was told to work elsewhere: %q", guidance)
	}
}
