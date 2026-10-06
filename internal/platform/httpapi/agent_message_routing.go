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

// resolveAgentMessageAudience uses the model's chosen audience. auto sends to
// a resolved recipient privately; without one the model must choose.
func resolveAgentMessageAudience(requested string, hasRecipient bool) (string, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	switch requested {
	case "private", "space":
		return requested, nil
	case "", "auto":
		if hasRecipient {
			return "private", nil
		}
		return "", serveragent.ErrInvalidRequest("choose audience private (with recipientUserId) or space; ask the user if their intent is unclear")
	}
	return "", serveragent.ErrInvalidRequest("audience must be auto, private, or space")
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
