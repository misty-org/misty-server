package api

import (
	"encoding/json"
)

func spaceSearchAgentToolSchema() json.RawMessage {
	return TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer"}}})
}
