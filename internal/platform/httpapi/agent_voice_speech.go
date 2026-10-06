package api

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

var agentSpeechPoint = regexp.MustCompile(`\[POINT:[^\]\r\n]*\]`)

func agentSpeechText(state string, events []db.AIInvocationEventRecord) (string, error) {
	if state != "completed" {
		return "", errors.New("reply is not complete")
	}
	text := ""
	for _, event := range events {
		if event.EventType != "assistant.message" {
			continue
		}
		var message struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(event.Payload, &message) != nil {
			return "", errors.New("invalid reply")
		}
		text = message.Text
	}
	text = strings.TrimSpace(agentSpeechPoint.ReplaceAllString(text, ""))
	if text == "" {
		return "", errors.New("empty reply")
	}
	// Spoken playback is bounded; the complete answer remains in the conversation.
	runes := []rune(text)
	if len(runes) > 5800 {
		text = string(runes[:5800]) + ". The rest of my answer is in the conversation."
	}
	return text, nil
}
