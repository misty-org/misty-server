package api

import (
	"fmt"
	"strings"
	"testing"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func historyTurns(count, size int) []db.AIConversationTurnRecord {
	base := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	turns := make([]db.AIConversationTurnRecord, count)
	for index := range turns {
		turns[index] = db.AIConversationTurnRecord{
			InvocationID: fmt.Sprintf("invocation_%02d", index),
			Prompt:       fmt.Sprintf("question %d %s", index, strings.Repeat("q", size)),
			Reply:        fmt.Sprintf("answer %d %s", index, strings.Repeat("a", size)),
			CreatedAt:    base.Add(time.Duration(index) * time.Minute),
		}
	}
	return turns
}

func TestConversationHistoryBudgetFollowsTheModelWindow(t *testing.T) {
	if got := conversationHistoryBudget(128_000); got != historyMaxRunes {
		t.Fatalf("128k window budget = %d", got)
	}
	if got := conversationHistoryBudget(32_000); got != 25_600 {
		t.Fatalf("32k window budget = %d", got)
	}
	if got := conversationHistoryBudget(8_000); got != historyMinRunes {
		t.Fatalf("small window budget = %d", got)
	}
}

func TestShortConversationsStayVerbatim(t *testing.T) {
	turns := historyTurns(5, 100)
	plan := planConversationHistory(turns, "invocation_04", nil, 20_000)
	if len(plan.summarize) != 0 || len(plan.recent) != 4 || plan.recent[3].InvocationID != "invocation_03" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestLongConversationsAgeOutTheOldestTurnsWithHeadroom(t *testing.T) {
	turns := historyTurns(30, 1_000)
	budget := 20_000
	plan := planConversationHistory(turns, "invocation_29", nil, budget)
	if len(plan.summarize) == 0 || plan.summarize[0].InvocationID != "invocation_00" {
		t.Fatalf("oldest turns were not summarized: %+v", plan.summarize)
	}
	if len(plan.summarize)+len(plan.recent) != 29 {
		t.Fatalf("turns lost: %d + %d", len(plan.summarize), len(plan.recent))
	}
	if size := len([]rune(renderConversationTurns(plan.recent))); size > budget/2 {
		t.Fatalf("verbatim history %d exceeds half the budget", size)
	}
	if plan.recent[len(plan.recent)-1].InvocationID != "invocation_28" {
		t.Fatal("latest turn was not kept verbatim")
	}
}

func TestStoredNotesCoverTheirTurns(t *testing.T) {
	turns := historyTurns(30, 1_000)
	notes := &db.AIConversationSummary{ThroughInvocationID: "invocation_19", ThroughCreatedAt: turns[19].CreatedAt, SummarizedTurns: 20, Summary: "notes"}
	plan := planConversationHistory(turns, "invocation_29", notes, 20_000)
	if plan.covered != 20 || len(plan.summarize) != 0 || len(plan.recent) != 9 || plan.recent[0].InvocationID != "invocation_20" {
		t.Fatalf("plan = covered %d, summarize %d, recent %d", plan.covered, len(plan.summarize), len(plan.recent))
	}
}

func TestATooLongLatestTurnIsStillKept(t *testing.T) {
	turns := historyTurns(3, 20_000)
	plan := planConversationHistory(turns, "current", nil, 12_000)
	if len(plan.recent) != 1 || plan.recent[0].InvocationID != "invocation_02" || len(plan.summarize) != 2 {
		t.Fatalf("plan = summarize %d recent %d", len(plan.summarize), len(plan.recent))
	}
}

func TestCompactedDividerFollowsTheLastSummarizedMessage(t *testing.T) {
	messages := []mistyConversationMessage{
		{ID: "invocation_01-user"}, {ID: "invocation_01-steering-2"}, {ID: "invocation_01-assistant"},
		{ID: "invocation_02-user"}, {ID: "invocation_02-assistant"},
	}
	markCompactedAfter(messages, "invocation_01")
	for index, message := range messages {
		if message.CompactedAfter != (index == 2) {
			t.Fatalf("message %d CompactedAfter = %v", index, message.CompactedAfter)
		}
	}
}
