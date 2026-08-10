package agent

import (
	"path/filepath"
	"strings"
)

func collectKnownPathsFromValue(session *Session, value any) {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"relativePath", "relative_path", "path", "name"} {
			if raw, ok := typed[key].(string); ok {
				collectKnownPathString(session, raw)
			}
		}
		for _, child := range typed {
			collectKnownPathsFromValue(session, child)
		}
	case []any:
		for _, child := range typed {
			collectKnownPathsFromValue(session, child)
		}
	}
}

func collectKnownPathString(session *Session, value string) {
	if normalized, ok := normalizeRelativePath(value); ok {
		session.KnownPaths[normalized] = struct{}{}
		return
	}
	root := strings.TrimRight(strings.TrimSpace(session.ActiveRoot), "/")
	candidate := strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if root != "" && strings.HasPrefix(candidate, root+"/") {
		if normalized, ok := normalizeRelativePath(strings.TrimPrefix(candidate, root+"/")); ok {
			session.KnownPaths[normalized] = struct{}{}
			return
		}
	}
	if base := filepath.Base(candidate); base != "." && base != "/" {
		if normalized, ok := normalizeRelativePath(base); ok {
			session.KnownPaths[normalized] = struct{}{}
		}
	}
}

type ErrInvalidRequest string

func (e ErrInvalidRequest) Error() string {
	return string(e)
}
