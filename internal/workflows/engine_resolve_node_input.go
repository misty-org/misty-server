package workflow

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func TestingResolveNodeInput(definition Definition, node Node, runInput json.RawMessage, outputs map[string]json.RawMessage, partials, skipped map[string]bool) (json.RawMessage, error) {
	value := map[string]any{}
	for name, binding := range node.Inputs {
		switch {
		case binding.SourceNode != "":
			if skipped[binding.SourceNode] {
				return nil, errBranchNotSelected
			}
			if partials[binding.SourceNode] && !node.Errors.AcceptsPartial {
				return nil, fmt.Errorf("upstream output %s is partial", binding.SourceNode)
			}
			raw, ok := outputs[binding.SourceNode]
			if !ok {
				return nil, fmt.Errorf("missing upstream output %s", binding.SourceNode)
			}
			decoded, portErr := workflowPortValue(raw, binding.SourcePort)
			if portErr != nil && conditionalWorkflowNode(definition, binding.SourceNode) && binding.SourcePort != "output" {
				return nil, errBranchNotSelected
			}
			if portErr != nil {
				return nil, portErr
			}
			value[name] = decoded
		case binding.InputPath != "":
			var root any
			if json.Unmarshal(runInput, &root) != nil {
				return nil, ErrInvalidDefinition
			}
			resolved, ok := workflowPathValue(root, binding.InputPath)
			if !ok {
				return nil, fmt.Errorf("missing workflow input %s", binding.InputPath)
			}
			value[name] = resolved
		default:
			value[name] = binding.Literal
		}
	}
	for _, edge := range definition.Edges {
		if edge.Target != node.ID {
			continue
		}
		if skipped[edge.Source] {
			return nil, errBranchNotSelected
		}
		if partials[edge.Source] && !node.Errors.AcceptsPartial {
			return nil, fmt.Errorf("upstream output %s is partial", edge.Source)
		}
		raw, ok := outputs[edge.Source]
		if !ok {
			return nil, fmt.Errorf("missing upstream output %s", edge.Source)
		}
		decoded, portErr := workflowPortValue(raw, edge.SourcePort)
		if portErr != nil && conditionalWorkflowNode(definition, edge.Source) && edge.SourcePort != "output" {
			return nil, errBranchNotSelected
		}
		if portErr != nil {
			return nil, portErr
		}
		value[edge.TargetPort] = decoded
	}
	if len(value) == 0 {
		if len(runInput) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return runInput, nil
	}
	raw, _ := json.Marshal(value)
	return raw, nil
}

func conditionalWorkflowNode(definition Definition, nodeID string) bool {
	for _, node := range definition.Nodes {
		if node.ID == nodeID {
			return node.Kind == "condition" || node.Kind == "switch"
		}
	}
	return false
}

func workflowPortValue(raw json.RawMessage, port string) (any, error) {
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		return nil, ErrOutputInvalid
	}
	if port == "" || port == "output" {
		return decoded, nil
	}
	value, ok := workflowPathValue(decoded, port)
	if !ok {
		return nil, fmt.Errorf("missing output port %s", port)
	}
	return value, nil
}

func workflowPathValue(root any, path string) (any, bool) {
	path = strings.Trim(strings.ReplaceAll(path, "/", "."), ".")
	if path == "" {
		return root, true
	}
	current := root
	for _, segment := range strings.Split(path, ".") {
		switch item := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = item[segment]
			if !ok {
				return nil, false
			}
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(item) {
				return nil, false
			}
			current = item[index]
		default:
			return nil, false
		}
	}
	return current, true
}

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
