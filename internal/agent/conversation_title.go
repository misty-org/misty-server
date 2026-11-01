package agent

import (
	"strings"
	"unicode"
)

// conversationTitleMaxRunes caps a chat title. The desktop's runtime names
// chats as their first run ends; the server cleans what it sends.
const conversationTitleMaxRunes = 48

// CleanConversationTitle keeps the first line, drops wrapping quotes and
// trailing punctuation, and caps the length on a word boundary.
func CleanConversationTitle(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	line = strings.TrimPrefix(strings.TrimSpace(line), "Title:")
	line = strings.Join(strings.Fields(line), " ")
	line = strings.Trim(line, "\"'“”‘’`*_ ")
	line = strings.TrimRightFunc(line, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
	if line == "" || strings.EqualFold(line, "none") {
		return ""
	}
	if runes := []rune(line); len(runes) > conversationTitleMaxRunes {
		cut := string(runes[:conversationTitleMaxRunes])
		if space := strings.LastIndex(cut, " "); space > 0 {
			cut = cut[:space]
		}
		line = strings.TrimRightFunc(cut, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
	}
	if runes := []rune(line); len(runes) > 0 {
		line = string(unicode.ToUpper(runes[0])) + string(runes[1:])
	}
	return line
}
