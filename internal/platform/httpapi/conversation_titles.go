package api

import (
	"context"
	"log"
	"strings"
	"time"

	agent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Conversation titles come from a separate, cheap model call that runs beside
// the agent's work and never delays it. The first try reads the opening
// message. When that is too vague to name ("hi"), a second try after Misty's
// first reply reads both. A title is saved only while the conversation still
// has its placeholder, so a person's own rename always wins, and the database
// tells every device through the "conversation-titles" account topic.

const conversationTitleTimeout = 20 * time.Second

// conversationTitleSlots bounds how many title calls run at once. A busy
// moment skips titling and leaves the readable placeholder in place.
var conversationTitleSlots = make(chan struct{}, 16)

// titleNewConversation starts the first try for a typed opening message.
func titleNewConversation(database *db.Database, models *agent.Service, userID, conversationID string, body aiInvocationInput) {
	if conversationID == "" || (body.Trigger != "message" && body.Trigger != "selection") {
		return
	}
	go titleConversation(database, models, userID, conversationID, body.Prompt, "")
}

// retitleAfterFirstReply is the second try, for an opening too vague to name.
func retitleAfterFirstReply(database *db.Database, models *agent.Service, userID string, body aiInvocationInput, answer string) {
	if body.ConversationID == "" || (body.Trigger != "message" && body.Trigger != "selection") {
		return
	}
	go titleConversation(database, models, userID, body.ConversationID, body.Prompt, answer)
}

func titleConversation(database *db.Database, models *agent.Service, userID, conversationID, prompt, reply string) {
	if database == nil || models == nil || strings.TrimSpace(prompt) == "" {
		return
	}
	select {
	case conversationTitleSlots <- struct{}{}:
		defer func() { <-conversationTitleSlots }()
	default:
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), conversationTitleTimeout)
	defer cancel()
	// Placeholders for this turn only: a later turn's prompt never matches the
	// title an earlier one set, so only a conversation's opening is named.
	fallback := cleanMistyTitle(prompt)
	placeholders := []string{"New conversation", fallback}
	current := database.ConversationTitle(ctx, userID, conversationID)
	if current != placeholders[0] && current != fallback {
		return
	}
	title, err := models.TitleConversation(ctx, userID, prompt, reply)
	if err != nil {
		log.Printf("conversation title for %s failed: %v", conversationID, err)
		return
	}
	if title == "" {
		// Nothing to name even after the reply: keep the opening words, not
		// "New conversation".
		if reply != "" && current == placeholders[0] {
			title = fallback
		} else {
			return
		}
	}
	if _, err := database.TitleAgentSessionIfUnchanged(ctx, userID, conversationID, title, placeholders); err != nil {
		log.Printf("saving conversation title for %s failed: %v", conversationID, err)
	}
}
