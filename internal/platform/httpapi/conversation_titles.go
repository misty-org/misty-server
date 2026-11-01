package api

import (
	"context"
	"log"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// The desktop's runtime names a new chat from its opening message as the run
// ends (docs/agents/runtime.md). A title is saved only while the conversation
// still has its placeholder, so a person's own rename always wins, and the
// database tells every device through the "conversation-titles" account topic.

// deviceTitlePrompt is the opening message a desktop runtime names the chat from, or "" when
// the chat needs no name: it was not typed, or it has a title already (a person's rename or an
// earlier turn's).
func deviceTitlePrompt(ctx context.Context, database *db.Database, userID, conversationID string, body aiInvocationInput) string {
	if conversationID == "" || strings.TrimSpace(body.Prompt) == "" || (body.Trigger != "message" && body.Trigger != "selection") {
		return ""
	}
	current := database.ConversationTitle(ctx, userID, conversationID)
	if current != "New conversation" && current != cleanMistyTitle(body.Prompt) {
		return ""
	}
	return body.Prompt
}

// saveDeviceTitle keeps the title a desktop runtime wrote, while the chat still has its
// placeholder. With nothing to name, the opening words replace "New conversation".
func saveDeviceTitle(ctx context.Context, database *db.Database, userID, conversationID string, body aiInvocationInput, written string) {
	if deviceTitlePrompt(ctx, database, userID, conversationID, body) == "" {
		return
	}
	fallback := cleanMistyTitle(body.Prompt)
	title := agent.CleanConversationTitle(written)
	if title == "" {
		title = fallback
	}
	if _, err := database.TitleAgentSessionIfUnchanged(ctx, userID, conversationID, title, []string{"New conversation", fallback}); err != nil {
		log.Printf("saving conversation title for %s failed: %v", conversationID, err)
	}
}
