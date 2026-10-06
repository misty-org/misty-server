package agenttools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

var errInvalidSchema = errors.New("invalid tool schema")

// compileSchema accepts only self-contained schemas without duplicate keys or
// prototype-polluting names, so every parser agrees on what a tool accepts.
func compileSchema(raw json.RawMessage) (*jsonschema.Resolved, error) {
	if len(raw) > 64<<10 {
		return nil, errInvalidSchema
	}
	var value map[string]any
	if err := decodeStrict(raw, &value); err != nil || value == nil {
		return nil, errInvalidSchema
	}
	var safe func(any, int) bool
	safe = func(v any, depth int) bool {
		if depth > 32 {
			return false
		}
		switch v := v.(type) {
		case map[string]any:
			for key, child := range v {
				if slices.Contains([]string{"__proto__", "constructor", "prototype", "$dynamicRef"}, key) {
					return false
				}
				if key == "$ref" {
					ref, ok := child.(string)
					if !ok || !strings.HasPrefix(ref, "#/$defs/") {
						return false
					}
				}
				if !safe(child, depth+1) {
					return false
				}
			}
		case []any:
			for _, child := range v {
				if !safe(child, depth+1) {
					return false
				}
			}
		}
		return true
	}
	if !safe(value, 0) {
		return nil, errInvalidSchema
	}
	var schema jsonschema.Schema
	if json.Unmarshal(raw, &schema) != nil {
		return nil, errInvalidSchema
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return nil, fmt.Errorf("%w: schema cannot be resolved", errInvalidSchema)
	}
	return resolved, nil
}

func decodeStrict(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > 2<<20 {
		return errInvalidSchema
	}
	// Reject duplicate object keys before decoding: signatures, schemas and host
	// parsers must agree about which value is being authorized.
	d := json.NewDecoder(bytes.NewReader(raw))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 40 {
			return errInvalidSchema
		}
		tok, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return err
				}
				key, ok := k.(string)
				if !ok || seen[key] {
					return errInvalidSchema
				}
				seen[key] = true
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errInvalidSchema
		}
		_, err = d.Token()
		return err
	}
	if err := walk(0); err != nil {
		return errInvalidSchema
	}
	if _, err := d.Token(); err != io.EOF {
		return errInvalidSchema
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("%w: %v", errInvalidSchema, err)
	}
	return nil
}
