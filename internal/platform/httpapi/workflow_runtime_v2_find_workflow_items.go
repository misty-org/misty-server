package api

import (
	"encoding/json"
	"strings"

)





func TestingDecodeJSONObject(value string) json.RawMessage {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```JSON")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)
	var object map[string]any
	if json.Unmarshal([]byte(trimmed), &object) != nil || object == nil {
		return nil
	}
	return json.RawMessage(trimmed)
}


func TestingWorkflowResourceIdentity(config, input json.RawMessage) (string, string) {
	var configValue, inputValue any
	_ = json.Unmarshal(config, &configValue)
	_ = json.Unmarshal(input, &inputValue)
	var find func(any) (string, string)
	find = func(value any) (string, string) {
		switch item := value.(type) {
		case map[string]any:
			provider, _ := item["providerId"].(string)
			resource, _ := item["resourceId"].(string)
			fingerprint, _ := item["fingerprint"].(string)
			if resource != "" {
				return provider + ":" + resource, fingerprint
			}
			for _, key := range []string{"destination", "relativePath", "channelId", "threadId"} {
				if text, ok := item[key].(string); ok && strings.TrimSpace(text) != "" {
					return key + ":" + strings.TrimSpace(text), fingerprint
				}
			}
			for _, child := range item {
				if key, childFingerprint := find(child); key != "" {
					return key, childFingerprint
				}
			}
		case []any:
			for _, child := range item {
				if key, childFingerprint := find(child); key != "" {
					return key, childFingerprint
				}
			}
		}
		return "", ""
	}
	if key, fingerprint := find(inputValue); key != "" {
		return key, fingerprint
	}
	return find(configValue)
}
