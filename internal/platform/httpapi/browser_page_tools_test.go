package api

import (
	"encoding/json"
	"strings"
	"testing"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestBrowserPageToolsAreDescribedWithStrictSchemas(t *testing.T) {
	found := map[string]string{}
	for _, descriptor := range browserToolDescriptors() {
		found[descriptor.Name] = descriptor.Risk
		if !browserPageReadTools[descriptor.Name] && !browserPageActTools[descriptor.Name] {
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(descriptor.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		if schema["additionalProperties"] != false || schema["type"] != "object" {
			t.Fatalf("%s schema must be a closed object", descriptor.Name)
		}
		// Anthropic rejects combinators at the top level of a tool schema.
		for _, combinator := range []string{"oneOf", "anyOf", "allOf"} {
			if _, ok := schema[combinator]; ok {
				t.Fatalf("%s uses %s at the top level", descriptor.Name, combinator)
			}
		}
	}
	for name := range browserPageReadTools {
		if found[name] != "read" {
			t.Fatalf("%s must be a read, got %q", name, found[name])
		}
		if !browserObservation(name) {
			t.Fatalf("%s failures must stay retryable observations", name)
		}
	}
	for name := range browserPageActTools {
		if found[name] != "write" {
			t.Fatalf("%s must be a write, got %q", name, found[name])
		}
	}
}

func TestBrowserGuidancePrefersSnapshotsAndKeepsBrowserActForVisualWork(t *testing.T) {
	guidance := agentCapabilityGuidance([]string{"browser.snapshot", "browser.steps", "browser.fill_form", "browser.act", "browser.navigate"}, false)
	for _, want := range []string{"browser_snapshot", "browser_steps", "browser_fill_form", "allowConsequential", "browser_act only for visual work"} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("guidance lacks %q: %s", want, guidance)
		}
	}
	if strings.Contains(guidance, "For anything you would do with a mouse or keyboard") {
		t.Fatal("snapshot runs must not send every action through browser_act")
	}
}

func TestBrowserExtractRequiresAnInstructionAndAnObjectSchema(t *testing.T) {
	valid := json.RawMessage(`{"scopeId":"scope-123","instruction":"List prices","schema":{"type":"object","properties":{"prices":{"type":"array"}}}}`)
	if _, _, err := validBrowserExtractRequest(valid); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"instruction":"","schema":{"type":"object"}}`,
		`{"instruction":"x","schema":{"type":"array"}}`,
		`{"instruction":"x"}`,
		`{"instruction":"x","schema":"object"}`,
		`{"instruction":"x","schema":{"type":"object","description":"` + strings.Repeat("a", 17<<10) + `"}}`,
	} {
		if _, _, err := validBrowserExtractRequest(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw[:min(len(raw), 80)])
		}
	}
}

func TestBrowserUploadNamesExactlyOneFileInput(t *testing.T) {
	for raw, want := range map[string]bool{
		`{"ref":"e4"}`:                     true,
		`{"elementRef":"element-1-2"}`:     true,
		`{"ref":"e4","elementRef":"el-1"}`: false,
		`{}`:                               false,
	} {
		if validBrowserUploadTarget(json.RawMessage(raw)) != want {
			t.Fatalf("%s: want %v", raw, want)
		}
	}
}

func TestDeviceGrantsAcceptThePageCapabilities(t *testing.T) {
	raw := json.RawMessage(`["browser.snapshot","browser.find","browser.extract","browser.wait_for","browser.steps","browser.fill_form"]`)
	if !db.DeviceContextCapabilitiesAllowed("browser_tab", raw) {
		t.Fatal("browser tabs must accept the page capabilities")
	}
}

func TestAgentSitesNormalizeToHostsDevicesMatch(t *testing.T) {
	for raw, want := range map[string]string{
		"example.com":                 "example.com",
		"https://www.Example.com/a?b": "example.com",
		"*.intranet.corp":             "intranet.corp",
		"mail.example.co.uk.":         "mail.example.co.uk",
	} {
		if got, ok := normalizeAgentSite(raw); !ok || got != want {
			t.Fatalf("%q → %q %v, want %q", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "localhost", "not a site", "exa_mple.com", "https://", strings.Repeat("a", 64) + ".com"} {
		if _, ok := normalizeAgentSite(raw); ok {
			t.Fatalf("accepted %q", raw)
		}
	}
}
