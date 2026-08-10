package unit

import (
	"encoding/json"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func suggestionMessage(id, user, text string) db.SpaceActionSuggestionContextMessage {
	return db.SpaceActionSuggestionContextMessage{ID: id, UserID: user, Content: []db.MessageSpan{{Type: "text", Text: text}}}
}

func TestActionSuggestionAgreementGateRequiresTwoPeople(t *testing.T) {
	if api.TestingActionSuggestionAgreementGate([]db.SpaceActionSuggestionContextMessage{suggestionMessage("1", "a", "Let's meet Friday"), suggestionMessage("2", "a", "Sounds good")}) {
		t.Fatal("one person's self-agreement must not pass")
	}
	if !api.TestingActionSuggestionAgreementGate([]db.SpaceActionSuggestionContextMessage{suggestionMessage("1", "a", "Let's meet Friday"), suggestionMessage("2", "b", "Sounds good")}) {
		t.Fatal("explicit proposal and acceptance by distinct people should pass")
	}
}

func TestNormalizeSuggestionModelResponseLocksCapabilities(t *testing.T) {
	actions, err := api.TestingNormalizeSuggestionModelResponse(`{"actions":[{"action_kind":"calendar.event.create","title":"Project review","summary":"Friday at 10","proposed_input":{"calendar_source_id":"misty"}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := actions[0].RequiredCapability; got != "calendar.events.create" {
		t.Fatalf("capability=%q", got)
	}
	var proposed map[string]any
	if err := json.Unmarshal(actions[0].ProposedInput, &proposed); err != nil {
		t.Fatal(err)
	}
	if proposed["calendar_source_id"] != "misty" {
		t.Fatalf("proposed=%v", proposed)
	}
}

func TestNormalizeSuggestionModelResponseRejectsUnknownAction(t *testing.T) {
	if _, err := api.TestingNormalizeSuggestionModelResponse(`{"actions":[{"action_kind":"files.delete","title":"Delete files","proposed_input":{}}]}`); err == nil {
		t.Fatal("unknown tools must be rejected")
	}
}

func TestNormalizeResourceAudience(t *testing.T) {
	space, err := db.NormalizeResourceAudience("", "")
	if err != nil || space.Kind != db.SpaceAudienceSpace {
		t.Fatalf("space=%+v err=%v", space, err)
	}
	private, err := db.NormalizeResourceAudience(db.SpaceAudienceConversation, "conversation_1")
	if err != nil || private.ConversationID != "conversation_1" {
		t.Fatalf("private=%+v err=%v", private, err)
	}
	if _, err := db.NormalizeResourceAudience(db.SpaceAudienceConversation, ""); err == nil {
		t.Fatal("conversation audience requires a conversation")
	}
	if _, err := db.NormalizeResourceAudience(db.SpaceAudienceSpace, "conversation_1"); err == nil {
		t.Fatal("space audience cannot retain a private conversation")
	}
}
