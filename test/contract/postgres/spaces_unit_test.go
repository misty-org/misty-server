package db

import (
	"errors"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestValidateSpaceMessageLimitsAndMentions(t *testing.T) {
	if err := TestingValidateMessage([]MessageSpan{{Type: "text", Text: "hello"}, {Type: "mention", UserID: "user", Label: "Sam"}, {Type: "link", Label: "Open source conversation", URL: "/spaces/space_1/chat?conversation=conversation_1"}}, []string{"one"}); err != nil {
		t.Fatal(err)
	}
	if err := TestingValidateMessage([]MessageSpan{{Type: "link", Label: "Unsafe", URL: "https://example.com"}}, nil); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("external message link error = %v", err)
	}
	if err := TestingValidateMessage([]MessageSpan{{Type: "text", Text: strings.Repeat("x", MaxMessageChars+1)}}, nil); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("oversized message error = %v", err)
	}
	if err := TestingValidateMessage([]MessageSpan{{Type: "mention", UserID: "user", AgentID: "agent"}}, nil); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("ambiguous mention error = %v", err)
	}
	if err := TestingValidateMessage([]MessageSpan{{Type: "text", Text: "hello"}}, []string{"1", "2", "3", "4", "5", "6"}); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("too many attachments error = %v", err)
	}
}
