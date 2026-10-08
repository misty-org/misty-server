package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"
)

func TestCursorCompanionDisplayValidation(t *testing.T) {
	image := companionTestCapture(t)
	body := aiInvocationInput{Mode: "companion", DisplayCaptures: []aiDisplayCapture{{aiCaptureAttachment: image, Screen: "screen1", Primary: true}}}
	if err := validateCompanionInput(&body); err != nil {
		t.Fatal(err)
	}
	if body.CompanionMode != "auto" {
		t.Fatal("omitted legacy mode must use the unified policy")
	}
	body.DisplayCaptures = append(body.DisplayCaptures, body.DisplayCaptures[0])
	if validateCompanionInput(&body) == nil {
		t.Fatal("duplicate screens accepted")
	}
	body.DisplayCaptures = nil
	body.CompanionMode = "question"
	if validateCompanionInput(&body) == nil {
		t.Fatal("unknown mode accepted")
	}
	body.Mode = "drawer"
	body.CompanionMode = "auto"
	if validateCompanionInput(&body) == nil {
		t.Fatal("companion policy leaked into legacy drawer")
	}
}
func TestCursorCompanionKeepsTenExchangesWithoutChangingStoredHistory(t *testing.T) {
	turns := make([]db.AIConversationTurnRecord, 12)
	for i := range turns {
		turns[i] = db.AIConversationTurnRecord{InvocationID: string(rune('a' + i)), Prompt: "question", Reply: "answer [POINT:10,20:button:screen1]"}
	}
	history := companionConversationHistory(turns, "l")
	if strings.Count(history, "User:") != 10 || strings.Contains(history, "[POINT:") {
		t.Fatal(history)
	}
	if !strings.Contains(turns[0].Reply, "[POINT:") {
		t.Fatal("historical record was mutated")
	}
}

func TestCursorCompanionRetainsFailedRequestForRetry(t *testing.T) {
	turns := []db.AIConversationTurnRecord{
		{InvocationID: "failed", Prompt: "Open example.com and report its heading", State: "failed", Failure: "Browser inspection unavailable"},
		{InvocationID: "current", Prompt: "try again", State: "running"},
	}
	history := companionConversationHistory(turns, "current")
	for _, expected := range []string{"Open example.com and report its heading", "Failed attempt", "Browser inspection unavailable"} {
		if !strings.Contains(history, expected) {
			t.Fatalf("missing %q in %q", expected, history)
		}
	}
	if strings.Contains(history, "try again") || turns[0].Reply != "" {
		t.Fatal("included current request or changed saved turn")
	}
}

func TestCursorCompanionBoundsFailedHistory(t *testing.T) {
	history := companionConversationHistory([]db.AIConversationTurnRecord{{
		InvocationID: "failed", Prompt: strings.Repeat("p", 20000), State: "failed",
		Failure: strings.Repeat("f", 20000),
	}}, "current")
	if len([]rune(history)) > 4000 || !strings.Contains(history, "Failed attempt") {
		t.Fatal("failed history must retain its status within the context budget")
	}
}
func TestCursorCompanionHasOneNaturalPolicy(t *testing.T) {
	team := companionSystemPrompt(aiInvocationInput{CompanionMode: "team"}, nil)
	auto := companionSystemPrompt(aiInvocationInput{CompanionMode: "auto"}, nil)
	if team != auto {
		t.Fatal("legacy modes must not choose different behavior")
	}
	for _, text := range []string{"do not need per-action approval", "integer pixel coordinates", "Capture the attached control surface again after every action", "multiple steps", "if Ask is enabled", "human confirmation", "teach instead of doing it", "[GUIDE:k/n]", "Examples:"} {
		if !strings.Contains(auto, text) {
			t.Fatal(text)
		}
	}
}

func TestCompanionTeachingTurnShowsWithoutTools(t *testing.T) {
	capture := companionTestCapture(t)
	body := aiInvocationInput{Mode: "companion", CompanionIntent: "teach", Timezone: "UTC", SurfaceID: "global", DisplayCaptures: []aiDisplayCapture{{aiCaptureAttachment: capture, Screen: "screen1", Primary: true}}}
	if err := validateCompanionInput(&body); err != nil {
		t.Fatal(err)
	}
	system := aiInvocationSystem(aiSystemPromptInput{body: body, agent: &db.AskIdentity{Name: "Misty"}, now: time.Unix(0, 0).UTC(), tools: []string{"browser.workspace.visual", "apps.search"}})
	for _, text := range []string{"teach instead of doing it", "This turn has no tools", "[POINT:x,y:label:screenN]", "Personal agent: Misty"} {
		if !strings.Contains(system, text) {
			t.Fatalf("teaching prompt lacks %q", text)
		}
	}
	for _, text := range []string{agentExecutionGuidance, "Do the requested work with your tools", "browser_act", "apps_search", "you do not need per-action approval"} {
		if strings.Contains(system, text) {
			t.Fatalf("teaching prompt must not carry %q", text)
		}
	}
	general := aiInvocationSystem(aiSystemPromptInput{body: aiInvocationInput{Mode: "companion", Timezone: "UTC", SurfaceID: "global"}, now: time.Unix(0, 0).UTC()})
	if !strings.Contains(general, "teach instead of doing it") || !strings.Contains(general, agentExecutionGuidance) {
		t.Fatal("general companion turns keep tools guidance and also teach")
	}
}

func TestCompanionLooksBeforeAnsweringAScreenQuestion(t *testing.T) {
	looking := companionSystemPrompt(aiInvocationInput{Mode: "companion"}, []string{screenLookTool})
	if !strings.Contains(looking, "call screen_look first") || strings.Contains(looking, "desktop context is unavailable") {
		t.Fatal("a desktop run without captures must look instead of giving up")
	}
	blind := companionSystemPrompt(aiInvocationInput{Mode: "companion"}, nil)
	if !strings.Contains(blind, "desktop context is unavailable") || strings.Contains(blind, "screen_look") {
		t.Fatal("a run that cannot look must say so")
	}
	capture := companionTestCapture(t)
	attached := companionSystemPrompt(aiInvocationInput{Mode: "companion", DisplayCaptures: []aiDisplayCapture{{aiCaptureAttachment: capture, Screen: "screen1", Primary: true}}}, []string{screenLookTool})
	if strings.Contains(attached, "call screen_look first") || strings.Contains(attached, "desktop context is unavailable") {
		t.Fatal("attached screens need neither")
	}
}

func TestCompanionTeachingIntentValidation(t *testing.T) {
	capture := companionTestCapture(t)
	screens := []aiDisplayCapture{{aiCaptureAttachment: capture, Screen: "screen1", Primary: true}}
	for _, body := range []aiInvocationInput{
		{Mode: "companion", CompanionIntent: "teach"},
		{Mode: "companion", CompanionIntent: "do", DisplayCaptures: screens},
		{Mode: "drawer", CompanionIntent: "teach"},
	} {
		if validateCompanionInput(&body) == nil {
			t.Fatalf("accepted %q intent in %s mode with %d screens", body.CompanionIntent, body.Mode, len(body.DisplayCaptures))
		}
	}
	if companionTeaches(aiInvocationInput{Mode: "drawer", CompanionIntent: "teach"}) || !companionTeaches(aiInvocationInput{Mode: "companion", CompanionIntent: "teach"}) {
		t.Fatal("only companion turns teach")
	}
}

func TestCompanionTeachingTurnsUseTheTeachingEffort(t *testing.T) {
	t.Setenv("MISTY_COMPANION_TEACH_REASONING", "")
	if companionAdmissionReasoning(aiInvocationInput{Mode: "companion", CompanionIntent: "teach"}, "high") != "low" {
		t.Fatal("a teaching turn must not inherit the conversation's Thinking effort")
	}
	if companionAdmissionReasoning(aiInvocationInput{Mode: "companion"}, "xhigh") != "xhigh" {
		t.Fatal("other turns keep the account's Thinking effort")
	}
}

func TestCompanionTeachReasoningDefaultsLow(t *testing.T) {
	t.Setenv("MISTY_COMPANION_TEACH_REASONING", "")
	if companionTeachReasoning() != "low" {
		t.Fatal("default teaching effort must be low")
	}
	t.Setenv("MISTY_COMPANION_TEACH_REASONING", "Medium")
	if companionTeachReasoning() != "medium" {
		t.Fatal("the eval's effort must be configurable")
	}
	t.Setenv("MISTY_COMPANION_TEACH_REASONING", "turbo")
	if companionTeachReasoning() != "low" {
		t.Fatal("invalid effort must fall back")
	}
}

func companionTestCapture(t *testing.T) aiCaptureAttachment {
	t.Helper()
	pixels := image.NewRGBA(image.Rect(0, 0, 16, 8))
	pixels.Set(3, 4, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, pixels); err != nil {
		t.Fatal(err)
	}
	return aiCaptureAttachment{ID: "one", Name: "screen1", MimeType: "image/png", DataURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes()), Width: 16, Height: 8, ContentHash: fmt.Sprintf("%x", sha256.Sum256(encoded.Bytes()))}
}
func TestCompanionRejectsInvalidPixels(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*aiCaptureAttachment)
	}{
		{"dimensions", func(c *aiCaptureAttachment) { c.Width++ }},
		{"checksum", func(c *aiCaptureAttachment) { c.ContentHash = "forged" }},
		{"mime", func(c *aiCaptureAttachment) {
			c.MimeType = "image/jpeg"
			c.DataURL = strings.Replace(c.DataURL, "image/png", "image/jpeg", 1)
		}},
		{"not an image", func(c *aiCaptureAttachment) {
			c.DataURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("png"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := companionTestCapture(t)
			test.mutate(&c)
			body := aiInvocationInput{Mode: "companion", DisplayCaptures: []aiDisplayCapture{{aiCaptureAttachment: c, Screen: "screen1", Primary: true}}}
			if validateCompanionInput(&body) == nil {
				t.Fatal("invalid pixels accepted")
			}
		})
	}
}

func TestDesktopControlRoutingUsesTheAttachedDevice(t *testing.T) {
	if invocationHasDesktopControl(aiInvocationInput{Mode: "companion"}) {
		t.Fatal("companion without a desktop must not claim desktop tools")
	}
	body := aiInvocationInput{Mode: "drawer", DeviceContexts: []aiInvocationDeviceContext{{Metadata: json.RawMessage(`{"desktop_control":true}`)}}}
	if !invocationHasDesktopControl(body) {
		t.Fatal("typed desktop chat needs the same control instructions")
	}
}

func TestCursorCompanionKeepsOnlyPlausibleMistyControls(t *testing.T) {
	controls := sanitizeScreenControls([]aiScreenControl{
		{Label: "  Manage   virtual windows ", Role: "button", X: 120, Y: 24, Width: 26, Height: 26},
		{Label: "", Role: "button", X: 1, Y: 1, Width: 1, Height: 1},
		{Label: "Off screen", Role: "button", X: 5000, Y: 24, Width: 26, Height: 26},
		{Label: "Flat", Role: "button", X: 10, Y: 10, Width: 0, Height: 26},
	}, 1512, 982)
	if len(controls) != 1 || controls[0].Label != "Manage virtual windows" {
		t.Fatalf("%+v", controls)
	}
	if sanitizeScreenControls([]aiScreenControl{{Label: "Off", Role: "button", X: -1, Y: 0, Width: 1, Height: 1}}, 10, 10) != nil {
		t.Fatal("a capture with no usable controls must carry none")
	}
}

func TestCursorCompanionAddsMistyGuideOnlyForMisty(t *testing.T) {
	other := aiInvocationInput{Mode: "companion", CompanionIntent: "teach", Prompt: "how do I commit in xcode?", DisplayCaptures: []aiDisplayCapture{{Screen: "screen1", Primary: true}}}
	if strings.Contains(companionSystemPrompt(other, nil), "Manage virtual windows") {
		t.Fatal("another app's question received the Misty guide")
	}
	shown := other
	shown.DisplayCaptures = []aiDisplayCapture{{Screen: "screen1", Primary: true, Controls: []aiScreenControl{{Label: "New tab", Role: "button", X: 1, Y: 1, Width: 1, Height: 1}}}}
	if !strings.Contains(companionSystemPrompt(shown, nil), "Manage virtual windows") {
		t.Fatal("a turn showing Misty's controls lacks the guide")
	}
	named := aiInvocationInput{Mode: "drawer", Prompt: "How do I create a virtual window in Misty?"}
	if !strings.Contains(aiInvocationSystem(aiSystemPromptInput{body: named, now: time.Now()}), "Manage virtual windows") {
		t.Fatal("a question naming Misty lacks the guide")
	}
}
