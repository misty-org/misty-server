package workflow

import (
	"crypto/sha256"
	"encoding/hex"
)

func schemaNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	default:
		return 0, false
	}
}

func schemaMap(value any) (JSONSchema, bool) {
	switch object := value.(type) {
	case JSONSchema:
		return object, true
	case map[string]any:
		return JSONSchema(object), true
	default:
		return nil, false
	}
}

func schemaStrings(value any) ([]string, bool) {
	switch items := value.(type) {
	case []string:
		return items, true
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, text)
		}
		return out, true
	default:
		return nil, false
	}
}

func schemaValues(value any) ([]any, bool) {
	if values, ok := value.([]any); ok {
		return values, true
	}
	if values, ok := value.([]string); ok {
		out := make([]any, len(values))
		for index := range values {
			out[index] = values[index]
		}
		return out, true
	}
	return nil, false
}

func idempotencyKey(runID, nodeID string) string {
	digest := sha256.Sum256([]byte(runID + ":" + nodeID))
	return hex.EncodeToString(digest[:])
}
