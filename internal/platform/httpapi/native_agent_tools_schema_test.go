package api

import (
	"encoding/json"
	"testing"
)

func TestNativeAgentToolSchemasRequiredIsArray(t *testing.T) {
	for _, descriptor := range append(nativeAgentToolDescriptors(), globalAgentSpaceDescriptors()...) {
		t.Run(descriptor.Name, func(t *testing.T) {
			var schema map[string]any
			if err := json.Unmarshal(descriptor.InputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			required, ok := schema["required"].([]any)
			if !ok {
				t.Fatalf("required must be an array, got %s", descriptor.InputSchema)
			}
			if descriptor.Name == "agents.list" && len(required) != 0 {
				t.Fatal("listing agents must accept an empty object")
			}
			if descriptor.Name == "agents.configure" && (len(required) != 1 || required[0] != "operation") {
				t.Fatal("configuration must still require operation")
			}
		})
	}
}
