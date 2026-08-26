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

func mistyAIContextReferences(references []mistyContextReference) []aiContextReference {
	result := make([]aiContextReference, 0, len(references))
	for _, reference := range references {
		id := strings.TrimSpace(reference.ID)
		kind := strings.ToLower(strings.TrimSpace(reference.Kind))
		if prefix := kind + ":"; strings.HasPrefix(strings.ToLower(id), prefix) {
			id = id[len(prefix):]
		}
		privacy := "private"
		if reference.SpaceID != "" {
			privacy = "shared"
		}
		result = append(result, aiContextReference{
			ID: id, Kind: kind, Title: reference.Title, Href: reference.Href,
			SpaceID: reference.SpaceID, Privacy: privacy, Attached: reference.Attached,
		})
	}
	return result
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

func mistyPromptWithContext(prompt string, references []mistyContextReference) string {
	if len(references) == 0 {
		return prompt
	}
	var context strings.Builder
	context.WriteString("Visible context labels (metadata only; do not imply file contents were read):\n")
	for _, reference := range references {
		title := strings.Join(strings.Fields(reference.Title), " ")
		if title == "" {
			continue
		}
		fmt.Fprintf(&context, "- %s: %s", reference.Kind, title)
		if reference.SpaceName != "" {
			fmt.Fprintf(&context, " in %s", strings.Join(strings.Fields(reference.SpaceName), " "))
		}
		context.WriteByte('\n')
	}
	context.WriteString("\nQuestion:\n")
	context.WriteString(prompt)
	return context.String()
}
