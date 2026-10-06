package workflow

import (
	"encoding/json"
)





func validateOutput(schema JSONSchema, output json.RawMessage) error {
	var value any
	if json.Unmarshal(output, &value) != nil {
		return ErrOutputInvalid
	}
	if !matchesSchema(schema, value) {
		return ErrOutputInvalid
	}
	return nil
}

// ValidateJSON is shared by coordinator-dispatched agent tool calls and normal
// graph continuation so both paths enforce the provider's published schema.
func ValidateJSON(schema JSONSchema, value json.RawMessage) error {
	return validateOutput(schema, value)
}

func matchesSchema(schema JSONSchema, value any) bool {
	if len(schema) == 0 {
		return true
	}
	if choices, ok := schemaValues(schema["enum"]); ok {
		matched := false
		for _, choice := range choices {
			left, _ := json.Marshal(choice)
			right, _ := json.Marshal(value)
			if string(left) == string(right) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	expected, _ := schema["type"].(string)
	switch expected {
	case "", "any":
		return true
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return false
		}
		if required, ok := schemaStrings(schema["required"]); ok {
			for _, name := range required {
				if object[name] == nil {
					return false
				}
			}
		}
		properties, _ := schemaMap(schema["properties"])
		for name, property := range properties {
			child, exists := object[name]
			if !exists {
				continue
			}
			childSchema, ok := schemaMap(property)
			if !ok || !matchesSchema(childSchema, child) {
				return false
			}
		}
		if additional, ok := schema["additionalProperties"].(bool); ok && !additional {
			for name := range object {
				if _, declared := properties[name]; !declared {
					return false
				}
			}
		}
		return true
	case "array":
		items, ok := value.([]any)
		if !ok {
			return false
		}
		if minimum, ok := schemaNumber(schema["minItems"]); ok && float64(len(items)) < minimum {
			return false
		}
		if maximum, ok := schemaNumber(schema["maxItems"]); ok && float64(len(items)) > maximum {
			return false
		}
		if raw, ok := schemaMap(schema["items"]); ok {
			for _, item := range items {
				if !matchesSchema(raw, item) {
					return false
				}
			}
		}
		return true
	case "string":
		text, ok := value.(string)
		if !ok {
			return false
		}
		if minimum, ok := schemaNumber(schema["minLength"]); ok && float64(len([]rune(text))) < minimum {
			return false
		}
		if maximum, ok := schemaNumber(schema["maxLength"]); ok && float64(len([]rune(text))) > maximum {
			return false
		}
		return true
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && number == float64(int64(number))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return false
	}
}
