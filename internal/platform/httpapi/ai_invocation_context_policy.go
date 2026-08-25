package api

import (
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func shouldRetrieveAccountContext(prompt string) bool {
	normalized := " " + strings.ToLower(strings.TrimSpace(prompt)) + " "
	if strings.TrimSpace(normalized) == "" {
		return false
	}
	markers := []string{
		" my ", " our ", " we ", " misty ", " workspace ", " account ", " space ",
		" task ", " tasks ", " todo ", " note ", " notes ", " file ", " files ",
		" project ", " projects ", " roadmap ", " message ", " messages ", " drawing ",
		" document ", " documents ", " launch plan ", " what did ", " where did ", " find ",
		" due ", " overdue ", " deadline ", " calendar ", " meeting ", " reminder ",
	}
	for _, marker := range markers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func boundedAIConversationHistory(turns []db.AIConversationTurnRecord, currentInvocationID string) string {
	const (
		maxTurns       = 12
		maxRunes       = 12_000
		maxPromptRunes = 1_200
		maxReplyRunes  = 2_400
	)
	entries := make([]string, 0, maxTurns)
	totalRunes := 0
	omitted := false
	for index := len(turns) - 1; index >= 0; index-- {
		turn := turns[index]
		prompt := strings.TrimSpace(turn.Prompt)
		if turn.InvocationID == currentInvocationID || prompt == "" {
			continue
		}
		entry := "User: " + truncateAgentRuntimeText(prompt, maxPromptRunes) + "\n"
		if reply := strings.TrimSpace(turn.Reply); reply != "" {
			entry += "Assistant: " + truncateAgentRuntimeText(reply, maxReplyRunes) + "\n"
		}
		entryRunes := len([]rune(entry))
		if len(entries) >= maxTurns || (len(entries) > 0 && totalRunes+entryRunes > maxRunes) {
			omitted = true
			break
		}
		entries = append(entries, entry)
		totalRunes += entryRunes
	}
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
	history := strings.Join(entries, "")
	if omitted {
		history = "[Earlier turns omitted to keep this conversation within its context budget.]\n" + history
	}
	return history
}
