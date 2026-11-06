package api

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

func validDeviceName(name string) bool {
	return validText(name, 1, 64) && !strings.ContainsFunc(name, func(value rune) bool { return value < 0x20 || value == 0x7f })
}

func validText(value string, min, max int) bool {
	length := utf8.RuneCountInString(strings.TrimSpace(value))
	return length >= min && length <= max
}

func validJSONObject(raw json.RawMessage) bool {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return false
	}
	_, ok := value.(map[string]any)
	return ok
}

func normalizedDevicePlatform(platform string) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "" {
		return "unknown"
	}
	for _, allowed := range []string{"macos", "windows", "linux", "unknown"} {
		if platform == allowed {
			return platform
		}
	}
	return ""
}

func containsLocalPath(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	return containsPathValue(value)
}

func containsPathValue(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
			if normalized == "path" || strings.HasSuffix(normalized, "path") {
				return true
			}
			if containsPathValue(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsPathValue(child) {
				return true
			}
		}
	}
	return false
}
