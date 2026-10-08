package api

import (
	"strings"
	"testing"
)

func TestCurrentTabGuidanceNamesThePageInFrontOfTheUser(t *testing.T) {
	if currentTabGuidance(nil, []string{screenOpenTool}) != "" {
		t.Fatal("a request without a current tab described one")
	}
	tab := &aiCurrentTab{Title: "Chess", URL: "https://chess.example/game/1"}
	guidance := currentTabGuidance(tab, []string{screenOpenTool})
	for _, want := range []string{"untrusted page data", "\"Chess\"", "https://chess.example/game/1", "target current_tab"} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("guidance %q is missing %q", guidance, want)
		}
	}
	if strings.Contains(currentTabGuidance(tab, nil), "screen_open") {
		t.Fatal("guidance promised screen_open to a run without it")
	}
}

func TestCurrentTabMustBeAWebPage(t *testing.T) {
	body := aiInvocationInput{Mode: "drawer", SurfaceID: "global", Trigger: "message", Prompt: "read this page",
		CurrentTab: &aiCurrentTab{Title: "Settings", URL: "file:///etc/passwd"}}
	if err := validateAIInvocationInput(&body); err == nil {
		t.Fatal("a non-web current tab was accepted")
	}
	body.CurrentTab = &aiCurrentTab{Title: " Docs ", URL: " https://docs.example "}
	if err := validateAIInvocationInput(&body); err != nil {
		t.Fatalf("a web page current tab was rejected: %v", err)
	}
	if body.CurrentTab.Title != "Docs" || body.CurrentTab.URL != "https://docs.example" {
		t.Fatalf("current tab was not trimmed: %#v", body.CurrentTab)
	}
}
