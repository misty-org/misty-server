package capabilities

import (
	"encoding/json"
	"errors"
)

var ErrRoutineReferenceMissing = errors.New("routine_reference_missing")

// These expressions are data references, not executable code. Admission validates
// the program; results come only from confirmed, protected provider receipts.
func RoutineResolveValue(raw, trigger json.RawMessage, steps map[string]json.RawMessage) (json.RawMessage, error) {
	var expression map[string]any
	if Decode(raw, &expression) != nil {
		return nil, ErrInvalid
	}
	value, err := routineValue(expression, trigger, steps, 0)
	if err != nil {
		return nil, err
	}
	result, err := json.Marshal(value)
	if err != nil || len(result) > 1<<20 {
		return nil, ErrInvalid
	}
	return result, nil
}
func routineValue(expression map[string]any, trigger json.RawMessage, steps map[string]json.RawMessage, depth int) (any, error) {
	if depth > 32 {
		return nil, ErrInvalid
	}
	switch expression["kind"] {
	case "literal":
		value, exists := expression["value"]
		if !exists {
			return nil, ErrInvalid
		}
		return value, nil
	case "reference":
		source, ok := expression["source"].(map[string]any)
		if !ok {
			return nil, ErrInvalid
		}
		raw := trigger
		if source["kind"] == "step" {
			id, _ := source["stepId"].(string)
			var exists bool
			raw, exists = steps[id]
			if !exists {
				return nil, ErrRoutineReferenceMissing
			}
		} else if source["kind"] != "trigger" {
			return nil, ErrInvalid
		}
		var item any
		if json.Unmarshal(raw, &item) != nil {
			return nil, ErrInvalid
		}
		path, ok := expression["path"].([]any)
		if !ok {
			return nil, ErrInvalid
		}
		for _, key := range path {
			switch key := key.(type) {
			case string:
				object, ok := item.(map[string]any)
				if !ok {
					return nil, ErrRoutineReferenceMissing
				}
				var exists bool
				item, exists = object[key]
				if !exists {
					return nil, ErrRoutineReferenceMissing
				}
			case float64:
				array, ok := item.([]any)
				if !ok || key < 0 || key != float64(int(key)) || int(key) >= len(array) {
					return nil, ErrRoutineReferenceMissing
				}
				item = array[int(key)]
			default:
				return nil, ErrInvalid
			}
		}
		return item, nil
	case "object":
		fields, ok := expression["fields"].(map[string]any)
		if !ok {
			return nil, ErrInvalid
		}
		result := map[string]any{}
		for key, child := range fields {
			child, ok := child.(map[string]any)
			if !ok {
				return nil, ErrInvalid
			}
			value, err := routineValue(child, trigger, steps, depth+1)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
		return result, nil
	case "array":
		items, ok := expression["items"].([]any)
		if !ok {
			return nil, ErrInvalid
		}
		result := []any{}
		for _, child := range items {
			child, ok := child.(map[string]any)
			if !ok {
				return nil, ErrInvalid
			}
			value, err := routineValue(child, trigger, steps, depth+1)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		return result, nil
	}
	return nil, ErrInvalid
}
func RoutineEvaluateCondition(raw, trigger json.RawMessage, steps map[string]json.RawMessage) (bool, error) {
	if len(raw) == 0 {
		return true, nil
	}
	var condition map[string]json.RawMessage
	if Decode(raw, &condition) != nil {
		return false, ErrInvalid
	}
	var kind string
	if json.Unmarshal(condition["kind"], &kind) != nil {
		return false, ErrInvalid
	}
	switch kind {
	case "equals":
		a, err := RoutineResolveValue(condition["left"], trigger, steps)
		if err != nil {
			return false, err
		}
		b, err := RoutineResolveValue(condition["right"], trigger, steps)
		return err == nil && EqualJSON(a, b), err
	case "exists":
		_, err := RoutineResolveValue(condition["reference"], trigger, steps)
		if errors.Is(err, ErrRoutineReferenceMissing) {
			return false, nil
		}
		return err == nil, err
	case "not":
		value, err := RoutineEvaluateCondition(condition["condition"], trigger, steps)
		return !value, err
	case "all", "any":
		var children []json.RawMessage
		if json.Unmarshal(condition["conditions"], &children) != nil {
			return false, ErrInvalid
		}
		for _, child := range children {
			value, err := RoutineEvaluateCondition(child, trigger, steps)
			if err != nil {
				return false, err
			}
			if kind == "all" && !value {
				return false, nil
			}
			if kind == "any" && value {
				return true, nil
			}
		}
		return kind == "all", nil
	}
	return false, ErrInvalid
}
