package capabilities

import (
	_ "embed"
	"encoding/json"
)

//go:embed browser-interaction.json
var browserInteractionJSON []byte

// BrowserInteractionSchema returns an isolated copy of the public SDK schema.
func BrowserInteractionSchema() json.RawMessage {
	return append(json.RawMessage(nil), browserInteractionJSON...)
}
