package api

import (
	"encoding/json"
	"strings"
)

// imagePayloadChars is how long a string must be before it is treated as an embedded
// image or file rather than text. Real text fields are far shorter.
const imagePayloadChars = 20_000

// admissionInput measures a model request for billing's admission hold: the text bytes,
// and the images counted apart. A screenshot embedded as base64 is megabytes of
// characters but a couple of thousand tokens, so sizing it by bytes would price one
// screenshot above a whole 5-hour window and refuse the run's next turn.
func admissionInput(raw []byte) map[string]any {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return map[string]any{"input_bytes": len(raw)}
	}
	bytes, images := 0, 0
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			if len(v) >= imagePayloadChars && (strings.HasPrefix(v, "data:") || looksEncoded(v)) {
				images++
				return
			}
			bytes += len(v)
		case []any:
			for _, item := range v {
				walk(item)
			}
		case map[string]any:
			for key, item := range v {
				bytes += len(key)
				walk(item)
			}
		}
	}
	walk(value)
	out := map[string]any{"input_bytes": bytes}
	if images > 0 {
		out["input_images"] = images
	}
	return out
}

// looksEncoded reports whether a long string is base64, judged by its start.
func looksEncoded(s string) bool {
	for _, r := range s[:256] {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '/' || r == '=' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
