package api

import (
	"fmt"
	"strings"
)

func cleanMistyTitle(value string) string {
	title := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if title == "" {
		return "New conversation"
	}
	const maxTitle = 64
	if len([]rune(title)) > maxTitle {
		return string([]rune(title)[:maxTitle]) + "…"
	}
	return title
}

func mergeAIResolvedContext(primary, secondary []aiResolvedContext, limit int) []aiResolvedContext {
	result := make([]aiResolvedContext, 0, min(limit, len(primary)+len(secondary)))
	seen := map[string]bool{}
	for _, group := range [][]aiResolvedContext{primary, secondary} {
		for _, item := range group {
			key := item.Citation.Kind + ":" + item.Citation.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, item)
			if len(result) == limit {
				return result
			}
		}
	}
	return result
}

func mistyAnswerCitations(answer string, resolved []aiResolvedContext) []aiCitation {
	result := []aiCitation{}
	for index, item := range resolved {
		if strings.Contains(answer, fmt.Sprintf("[%d]", index+1)) {
			result = append(result, item.Citation)
		}
	}
	return result
}
