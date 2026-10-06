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
	team := companionSystemPrompt(aiInvocationInput{CompanionMode: "team"})
	auto := companionSystemPrompt(aiInvocationInput{CompanionMode: "auto"})
	if team != auto {
		t.Fatal("legacy modes must not choose different behavior")
	}
	for _, text := range []string{"do not need per-action approval", "actual pixels", "Capture the attached control surface again after every action", "multiple steps", "if Ask is enabled", "human confirmation"} {
		if !strings.Contains(auto, text) {
			t.Fatal(text)
		}
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
