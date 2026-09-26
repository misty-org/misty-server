package api

import (
	"encoding/base64"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"strings"
	"testing"
)

func TestCursorCompanionDisplayValidation(t *testing.T) {
	image := aiCaptureAttachment{ID: "one", Name: "screen1", MimeType: "image/jpeg", DataURL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("jpeg")), Width: 1280, Height: 720, ContentHash: "hash"}
	body := aiInvocationInput{Mode: "companion", DisplayCaptures: []aiDisplayCapture{{aiCaptureAttachment: image, Screen: "screen1", Primary: true}}}
	if err := validateCompanionInput(&body); err != nil {
		t.Fatal(err)
	}
	if body.CompanionMode != "team" {
		t.Fatal("legacy caller must default to Team")
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
func TestCursorCompanionTeamAndAutoPolicies(t *testing.T) {
	team := companionSystemPrompt(aiInvocationInput{CompanionMode: "team"})
	auto := companionSystemPrompt(aiInvocationInput{CompanionMode: "auto"})
	for _, text := range []string{"Do not take unsolicited actions", "Questions about modes do not change mode", "actual pixels", "browser.visual"} {
		if !strings.Contains(team, text) {
			t.Fatal(text)
		}
	}
	if !strings.Contains(auto, "multiple steps") || strings.Contains(auto, "Team mode:") {
		t.Fatal(auto)
	}
}
