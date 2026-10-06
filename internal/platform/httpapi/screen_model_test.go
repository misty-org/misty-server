package api

import (
	"encoding/json"
	"testing"
)

func TestScreenModelMessagesKeepSystemTextAndInlineScreenshots(t *testing.T) {
	system, messages, err := screenModelMessages(json.RawMessage(`[
		{"role":"system","content":"Plan one action."},
		{"role":"user","content":[{"type":"text","text":"Draw a house"},{"type":"image_url","image_url":{"url":"data:image/png;base64,cG5n"}}]},
		{"role":"assistant","content":"<action>click</action>"}
	]`))
	if err != nil || system != "Plan one action." || len(messages) != 2 {
		t.Fatalf("messages = %q %+v %v", system, messages, err)
	}
	user := messages[0]
	if user.Role != "user" || len(user.Content) != 2 || user.Content[1].Type != "image" || user.Content[1].MediaType != "image/png" || user.Content[1].Data != "cG5n" {
		t.Fatalf("screenshot not passed inline: %+v", user)
	}
	if messages[1].Role != "assistant" || messages[1].Content[0].Text != "<action>click</action>" {
		t.Fatalf("history lost: %+v", messages[1])
	}
}

func TestScreenModelMessagesRefuseRemoteImagesAndUnknownRoles(t *testing.T) {
	for _, raw := range []string{
		`[]`,
		`[{"role":"system","content":"only instructions"}]`,
		`[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/shot.png"}}]}]`,
		`[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/svg+xml;base64,PHN2Zz4="}}]}]`,
		`[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,***"}}]}]`,
		`[{"role":"tool","content":"result"}]`,
		`[{"role":"user","content":[{"type":"input_audio"}]}]`,
	} {
		if _, _, err := screenModelMessages(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
