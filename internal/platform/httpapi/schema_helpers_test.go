package api

import (
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"
)

// compileTestSchema resolves a tool schema the way the agent toolbox does.
func compileTestSchema(raw json.RawMessage) (*jsonschema.Resolved, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	return schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
}
