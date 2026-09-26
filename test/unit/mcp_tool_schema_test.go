package unit

import (
	"encoding/json"
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	"testing"
)

func TestMCPToolSchemaAcceptsBoundedCompositions(t *testing.T) {
	t.Parallel()
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"input":{"anyOf":[{"type":"string"},{"type":"null"}]},
			"values":{"type":"object","additionalProperties":{"type":"string"}}
		}
	}`)
	if err := api.TestingValidateMCPToolSchema(schema); err != nil {
		t.Fatalf("expected composed schema to be accepted: %v", err)
	}
}

func TestMCPToolSchemaRejectsExternalReferences(t *testing.T) {
	t.Parallel()
	schema := json.RawMessage(`{"type":"object","properties":{"input":{"$ref":"https://example.com/schema.json"}}}`)
	if err := api.TestingValidateMCPToolSchema(schema); err == nil {
		t.Fatal("expected external schema reference to be rejected")
	}
}
