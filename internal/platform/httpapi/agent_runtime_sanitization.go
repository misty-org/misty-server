package api

import (
	"encoding/json"
	"strings"
)

const maximumAgentLifecycleStringRunes = 2_000

func sanitizeAgentLifecycleJSON(raw json.RawMessage) json.RawMessage {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return json.RawMessage(`{}`)
	}
	value = sanitizeAgentLifecycleValue(value, "")
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func sanitizeAgentLifecycleValue(value any, key string) any {
	normalizedKey := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
	for _, sensitive := range []string{"authorization", "cookie", "credential", "password", "secret", "token", "api_key", "apikey"} {
		if strings.Contains(normalizedKey, sensitive) {
			return "[redacted]"
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			out[childKey] = sanitizeAgentLifecycleValue(child, childKey)
		}
		return out
	case []any:
		limit := len(typed)
		if limit > 100 {
			limit = 100
		}
		out := make([]any, 0, limit)
		for _, child := range typed[:limit] {
			out = append(out, sanitizeAgentLifecycleValue(child, key))
		}
		return out
	case string:
		runes := []rune(typed)
		if len(runes) > maximumAgentLifecycleStringRunes {
			return string(runes[:maximumAgentLifecycleStringRunes]) + "…"
		}
		return typed
	default:
		return typed
	}
}

func TestingSanitizeAgentLifecycleJSON(raw json.RawMessage) json.RawMessage {
	return sanitizeAgentLifecycleJSON(raw)
}
