package api

import "strings"

func compileAIInvocationPrompt(body aiInvocationInput, resolved []aiResolvedContext) string {
	prompt := aiContextPrompt(body.Prompt, resolved, body.Selection, body.Context)
	if body.Trigger == "schedule" {
		prompt = "This is an explicitly enabled recurring personal briefing. Include [N] citations for factual claims.\n\n" + prompt
	}
	artifactKind := strings.TrimSpace(body.RequestedArtifactKind)
	if artifactKind == "" {
		artifactKind = strings.TrimSpace(inferredAIArtifactKind(body))
	}
	if artifactKind == "" && body.Selection != nil && strings.HasPrefix(body.SurfaceID, "notes") {
		artifactKind = "text_patch"
	}
	switch artifactKind {
	case "text_patch":
		return "Rewrite the selected content to satisfy the user request. Return only the replacement text, with no preface, quotation marks, or Markdown fence.\n\n" + prompt
	case "task_set":
		return "Extract concrete, non-duplicative tasks from the authorized content. Return strict JSON only in this shape: {\"tasks\":[{\"title\":\"...\",\"notes\":\"...\",\"priority\":\"high|medium|low\"}]}. Use at most 20 tasks. Do not invent owners, dates, or commitments. Return {\"tasks\":[]} when there are no concrete tasks.\n\n" + prompt
	default:
		if spec, ok := aiArtifactSpecs[artifactKind]; ok {
			return spec.Prompt + " Return strict JSON only in this shape: {\"summary\":\"short review summary\",\"operations\":" + spec.Shape + "}. Do not claim the proposal was applied or executed.\n\n" + prompt
		}
	}
	return prompt
}
