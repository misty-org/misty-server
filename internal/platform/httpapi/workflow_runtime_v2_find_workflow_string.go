package api

import (
	"strings"

)

func TestingFindWorkflowString(value any, keys ...string) string {
	wanted := map[string]bool{}
	for _, key := range keys {
		wanted[key] = true
	}
	var find func(any) string
	find = func(current any) string {
		switch item := current.(type) {
		case map[string]any:
			for key, child := range item {
				if wanted[key] {
					if text, ok := child.(string); ok && strings.TrimSpace(text) != "" {
						return strings.TrimSpace(text)
					}
				}
			}
			for _, child := range item {
				if found := find(child); found != "" {
					return found
				}
			}
		case []any:
			for _, child := range item {
				if found := find(child); found != "" {
					return found
				}
			}
		}
		return ""
	}
	return find(value)
}



