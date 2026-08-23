package api

import (
	"net/url"
	"strings"
)

func inferredAIArtifactKind(body aiInvocationInput) string {
	prompt := strings.ToLower(strings.TrimSpace(body.Prompt))
	if prompt == "" {
		return ""
	}
	has := func(words ...string) bool {
		for _, word := range words {
			if strings.Contains(prompt, word) {
				return true
			}
		}
		return false
	}
	switch body.SurfaceID {
	case "notes":
		if body.Selection != nil && has("rewrite", "improve", "shorten", "expand", "fix", "change") {
			return "text_patch"
		}
		if has("extract task", "turn into task", "make task") {
			return "task_set"
		}
	case "planner.tasks":
		if has("create", "add", "break down", "plan", "extract") {
			return "task_set"
		}
	case "planner.agenda":
		if has("schedule", "create event", "add event", "calendar") {
			return "calendar_event"
		}
	case "planner.roadmap":
		if has("add", "change", "connect", "update", "create") {
			return "roadmap_patch"
		}
	case "drawings":
		if has("add", "draw", "arrange", "cluster", "move", "diagram") {
			return "drawing_patch"
		}
	case "inbox":
		if has("draft", "reply", "compose", "write email") {
			return "mail_draft"
		}
	case "space.chat":
		if has("draft", "reply", "write message") {
			return "message_draft"
		}
	case "code":
		if has("edit", "fix", "refactor", "implement", "change") {
			return "code_patch"
		}
	case "terminal":
		if has("run", "execute", "command", "install") {
			return "terminal_command"
		}
	case "browser":
		if has("click", "navigate", "fill", "submit", "upload", "open page") {
			return "browser_action"
		}
	case "transfers":
		if has("retry", "resume", "cancel", "recover") {
			return "transfer_plan"
		}
	case "extensions":
		if has("install", "configure", "run", "enable") {
			return "extension_action"
		}
	case "photo-editor":
		if has("edit", "enhance", "bright", "contrast", "filter", "warm", "cool") {
			return "image_edit"
		}
	}
	return ""
}

func aiConciseSummary(value string) string {
	clean := strings.Join(strings.Fields(value), " ")
	runes := []rune(clean)
	if len(runes) > 240 {
		return string(runes[:239]) + "…"
	}
	return clean
}

func firstAIContextSpace(context []aiContextReference) string {
	for _, reference := range context {
		if value := strings.TrimSpace(reference.SpaceID); value != "" {
			return value
		}
	}
	return ""
}

func firstAIContextHref(context []aiContextReference) string {
	for _, reference := range context {
		if value := strings.TrimSpace(reference.Href); value != "" {
			return value
		}
	}
	return ""
}

func aiInvocationPrivacyBoundary(context []aiContextReference) string {
	if spaceID := firstAIContextSpace(context); spaceID != "" {
		return "shared:" + spaceID
	}
	for _, reference := range context {
		if reference.Privacy == "device" || reference.Privacy == "private" {
			return "private"
		}
		if reference.Privacy == "provider" {
			return "provider"
		}
	}
	return "account"
}

func aiSelectionCitation(body aiInvocationInput) *aiCitation {
	if body.Selection == nil || body.Selection.Object["kind"] != "browser-page" {
		return nil
	}
	scopeID, _ := body.Selection.Object["id"].(string)
	if strings.TrimSpace(scopeID) == "" {
		return nil
	}
	for _, reference := range body.Context {
		if reference.Kind != "browser-tab" || reference.OpaqueScopeID != scopeID || !reference.Attached {
			continue
		}
		return &aiCitation{
			ID: scopeID, Kind: "browser-page", Title: firstAIText(reference.Title, "Browser page"),
			Href: "misty://browser/" + url.PathEscape(scopeID), Revision: reference.Revision,
			Excerpt: aiExcerpt(body.Selection.Content),
		}
	}
	return nil
}
