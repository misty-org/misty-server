package api

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

var aiWindowsPathPattern = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

func validateAIContextReferences(references []aiContextReference) error {
	for index := range references {
		reference := &references[index]
		reference.Kind = strings.TrimSpace(reference.Kind)
		reference.ID = strings.TrimSpace(reference.ID)
		reference.Title = strings.TrimSpace(reference.Title)
		reference.Privacy = strings.TrimSpace(reference.Privacy)
		reference.SpaceID = strings.TrimSpace(reference.SpaceID)
		reference.OpaqueScopeID = strings.TrimSpace(reference.OpaqueScopeID)
		if reference.Kind == "" || reference.ID == "" || reference.Title == "" {
			return errors.New("context references require kind, id, and title")
		}
		switch reference.Privacy {
		case "private":
		case "shared":
			if reference.SpaceID == "" {
				return errors.New("shared context requires a Space identity")
			}
		case "device", "provider":
			if reference.OpaqueScopeID == "" {
				return errors.New("device and provider context require an opaque scope")
			}
		default:
			return errors.New("context privacy class is invalid")
		}
		if looksLikeRawLocalPath(reference.ID) || looksLikeRawLocalPath(reference.OpaqueScopeID) {
			return errors.New("raw local paths are not valid AI context references")
		}
		metadata, err := json.Marshal(reference.Metadata)
		if err != nil || len(metadata) > 8<<10 || containsRawLocalPath(reference.Metadata) {
			return errors.New("context metadata is invalid or too large")
		}
	}
	return nil
}

func validateAISelectionAnchor(selection *aiSelectionSnapshot) error {
	if selection == nil {
		return nil
	}
	if len(selection.Content) > maxAISelectionBytes || strings.TrimSpace(selection.ContentHash) == "" {
		return errors.New("selection is invalid or too large")
	}
	if kind, _ := selection.Object["kind"].(string); strings.TrimSpace(kind) == "" {
		return errors.New("selection object kind is required")
	}
	if id, _ := selection.Object["id"].(string); strings.TrimSpace(id) == "" || looksLikeRawLocalPath(id) {
		return errors.New("selection object id is invalid")
	}
	if containsRawLocalPath(selection.Anchors) {
		return errors.New("selection anchors cannot contain raw local paths")
	}
	return nil
}

func containsRawLocalPath(values map[string]any) bool {
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			if looksLikeRawLocalPath(typed) {
				return true
			}
		case map[string]any:
			if containsRawLocalPath(typed) {
				return true
			}
		}
	}
	return false
}

func looksLikeRawLocalPath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	return strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~/") ||
		strings.HasPrefix(value, `\\`) || aiWindowsPathPattern.MatchString(value) ||
		strings.HasPrefix(lower, "file://")
}
