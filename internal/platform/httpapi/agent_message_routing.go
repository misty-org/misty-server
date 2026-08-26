package api

import (
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func resolveAgentMessageRecipient(members []db.SpaceMember, actorUserID, requestedUserID, prompt, message string) (*db.SpaceMember, error) {
	if requestedUserID != "" {
		for index := range members {
			if members[index].UserID == requestedUserID && requestedUserID != actorUserID {
				return &members[index], nil
			}
		}
		return nil, serveragent.ErrInvalidRequest("recipientUserId must identify another member of this Space")
	}
	combined := prompt + " " + message
	matches := []db.SpaceMember{}
	for _, member := range members {
		if member.UserID != actorUserID && member.Name != "" && containsGroundingPhrase(combined, member.Name) {
			matches = append(matches, member)
		}
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}
	return nil, nil
}

func resolveAgentMessageAudience(prompt, requested string, hasRecipient bool) (string, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		requested = "auto"
	}
	if requested != "auto" && requested != "private" && requested != "space" {
		return "", serveragent.ErrInvalidRequest("audience must be auto, private, or space")
	}
	for _, phrase := range []string{"dm", "direct message", "private message", "privately", "one on one", "one-to-one"} {
		if containsGroundingPhrase(prompt, phrase) {
			return "private", nil
		}
	}
	for _, phrase := range []string{"everyone", "everybody", "all members", "whole team", "group chat", "shared chat", "space chat", "team chat", "in the group", "post", "announce"} {
		if containsGroundingPhrase(prompt, phrase) {
			return "space", nil
		}
	}
	if requested != "auto" {
		return requested, nil
	}
	if hasRecipient {
		return "private", nil
	}
	return "", serveragent.ErrInvalidRequest("Ask the user before sending: Should I send this privately or in the Space chat?")
}

func agentMessageContent(message string, recipient *db.SpaceMember) []db.MessageSpan {
	if recipient == nil || strings.TrimSpace(recipient.Name) == "" {
		return []db.MessageSpan{{Type: "text", Text: message}}
	}
	needle := "@" + recipient.Name
	lowerMessage, lowerNeedle := strings.ToLower(message), strings.ToLower(needle)
	content := []db.MessageSpan{}
	searchFrom, emittedThrough := 0, 0
	for {
		relative := strings.Index(lowerMessage[searchFrom:], lowerNeedle)
		if relative < 0 {
			break
		}
		index := searchFrom + relative
		end := index + len(needle)
		if end < len(message) {
			next := rune(message[end])
			if (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z') || (next >= '0' && next <= '9') || next == '_' {
				searchFrom = end
				continue
			}
		}
		if index > emittedThrough {
			content = append(content, db.MessageSpan{Type: "text", Text: message[emittedThrough:index]})
		}
		content = append(content, db.MessageSpan{Type: "mention", UserID: recipient.UserID, Label: recipient.Name})
		searchFrom, emittedThrough = end, end
	}
	if emittedThrough == 0 {
		return []db.MessageSpan{{Type: "text", Text: message}}
	}
	if emittedThrough < len(message) {
		content = append(content, db.MessageSpan{Type: "text", Text: message[emittedThrough:]})
	}
	return content
}
