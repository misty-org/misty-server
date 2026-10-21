package api

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

var agentSpeechPoint = regexp.MustCompile(`\[POINT:[^\]\r\n]*\]`)

// agentSpeechGuide marks a walkthrough step; it is for the pointer, not the ear.
var agentSpeechGuide = regexp.MustCompile(`\[GUIDE:\d+/\d+\]`)

// agentSpeechLessonTags are Teach mode's lesson, route and focus tags (ai_teach_mode.go):
// for the companion and the device's lesson state, never the ear.
var agentSpeechLessonTags = regexp.MustCompile(`\[(?:ROUTE|FOCUS):[^\]\r\n]*\]|\[(?:LESSON|COURSE):[^\r\n]*\]`)

// agentSpeechSketch is a drawing (companion_sketch_prompt.txt): for the eyes, never the
// ear. An unclosed block at the end is a drawing still streaming.
var agentSpeechSketch = regexp.MustCompile("(?s)```misty-sketch.*?(?:```|$)")

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
	text = strings.TrimSpace(agentSpeechGuide.ReplaceAllString(agentSpeechPoint.ReplaceAllString(agentSpeechLessonTags.ReplaceAllString(agentSpeechSketch.ReplaceAllString(text, ""), ""), ""), ""))
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
