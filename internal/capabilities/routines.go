package capabilities

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

//go:embed routine-definition.json
var routineDefinitionJSON []byte
var routineSchema = sync.OnceValues(func() (*jsonschema.Resolved, error) { return CompileSchema(routineDefinitionJSON) })

type RoutineDefinition struct {
	Protocol    int             `json:"protocol"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	SpaceID     string          `json:"spaceId,omitempty"`
	Trigger     json.RawMessage `json:"trigger"`
	Steps       []RoutineStep   `json:"steps"`
	Budget      struct {
		ModelTurns    int `json:"modelTurns"`
		ActiveSeconds int `json:"activeSeconds"`
	} `json:"budget"`
}
type RoutinePin struct {
	Capability        string `json:"capability"`
	CapabilityVersion int    `json:"capabilityVersion"`
	ProviderID        string `json:"providerId"`
	ProviderVersion   int    `json:"providerVersion"`
	TargetID          string `json:"targetId"`
	TargetRevision    int    `json:"targetRevision"`
}
type RoutineStep struct {
	ID           string          `json:"id"`
	Kind         string          `json:"kind"`
	Label        string          `json:"label"`
	When         json.RawMessage `json:"when,omitempty"`
	Action       *RoutinePin     `json:"action,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	AllowPartial bool            `json:"allowPartial,omitempty"`
	Prompt       json.RawMessage `json:"prompt,omitempty"`
	Actions      []RoutinePin    `json:"actions,omitempty"`
	MaxTurns     int             `json:"maxTurns,omitempty"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Until        json.RawMessage `json:"until,omitempty"`
}

// ParseRoutineDefinition returns normalized JSON for immutable storage. Defaults
// match the public SDK. Definitions remain inert data; validation grants nothing.
func ParseRoutineDefinition(raw json.RawMessage) (*RoutineDefinition, json.RawMessage, error) {
	if len(raw) > 1<<20 {
		return nil, nil, ErrInvalid
	}
	var value map[string]any
	if Decode(raw, &value) != nil || value == nil {
		return nil, nil, ErrInvalid
	}
	if !routineTreeBounded(value) {
		return nil, nil, ErrInvalid
	}
	setDefault(value, "description", "")
	if budget, ok := value["budget"].(map[string]any); ok {
		setDefault(budget, "modelTurns", float64(20))
		setDefault(budget, "activeSeconds", float64(1800))
	}
	if trigger, ok := value["trigger"].(map[string]any); ok && trigger["kind"] == "app_event" {
		setDefault(trigger, "resourceIds", []any{})
	}
	steps, _ := value["steps"].([]any)
	for _, item := range steps {
		if step, ok := item.(map[string]any); ok && step["kind"] == "capability" {
			setDefault(step, "allowPartial", false)
		}
	}
	schema, err := routineSchema()
	if err != nil {
		return nil, nil, err
	}
	if schema.Validate(value) != nil {
		return nil, nil, ErrInvalid
	}
	normalized, err := json.Marshal(value)
	if err != nil || len(normalized) > 1<<20 {
		return nil, nil, ErrInvalid
	}
	var result RoutineDefinition
	if Decode(normalized, &result) != nil {
		return nil, nil, ErrInvalid
	}
	seen := map[string]bool{}
	turns := 0
	for _, step := range result.Steps {
		if seen[step.ID] {
			return nil, nil, ErrInvalid
		}
		if !routineReferencesValid(step.When, seen) {
			return nil, nil, ErrInvalid
		}
		expression := step.Input
		switch step.Kind {
		case "agent":
			turns += step.MaxTurns
			expression = step.Prompt
			if _, err := CompileSchema(step.OutputSchema); err != nil {
				return nil, nil, err
			}
		case "wait":
			expression = step.Until
		}
		if !routineReferencesValid(expression, seen) {
			return nil, nil, ErrInvalid
		}
		seen[step.ID] = true
	}
	if turns > result.Budget.ModelTurns || !routineTriggerValid(result.Trigger) {
		return nil, nil, ErrInvalid
	}
	return &result, normalized, nil
}
func setDefault(value map[string]any, key string, fallback any) {
	if _, ok := value[key]; !ok {
		value[key] = fallback
	}
}
func routineReferencesValid(raw json.RawMessage, seen map[string]bool) bool {
	if len(raw) == 0 {
		return true
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	var walk func(any) bool
	walk = func(item any) bool {
		switch item := item.(type) {
		case map[string]any:
			switch item["kind"] {
			case "literal":
				return true
			case "reference":
				source, _ := item["source"].(map[string]any)
				return source["kind"] == "trigger" || source["kind"] == "step" && seen[source["stepId"].(string)]
			case "object":
				if fields, ok := item["fields"].(map[string]any); !ok || len(fields) > 100 {
					return false
				}
			}
			for _, child := range item {
				if !walk(child) {
					return false
				}
			}
		case []any:
			for _, child := range item {
				if !walk(child) {
					return false
				}
			}
		}
		return true
	}
	return walk(value)
}
func routineTriggerValid(raw json.RawMessage) bool {
	var trigger struct {
		Kind     string `json:"kind"`
		Timezone string `json:"timezone"`
		Days     []int  `json:"daysOfWeek"`
		Times    []struct {
			Hour   int `json:"hour"`
			Minute int `json:"minute"`
		} `json:"times"`
	}
	if json.Unmarshal(raw, &trigger) != nil {
		return false
	}
	if trigger.Kind != "schedule" {
		return true
	}
	// Local is machine-dependent and is not an IANA timezone accepted by the SDK.
	if trigger.Timezone == "" || trigger.Timezone == "Local" {
		return false
	}
	if _, err := time.LoadLocation(trigger.Timezone); err != nil {
		return false
	}
	days := map[int]bool{}
	times := map[int]bool{}
	for _, day := range trigger.Days {
		if days[day] {
			return false
		}
		days[day] = true
	}
	for _, clock := range trigger.Times {
		minute := clock.Hour*60 + clock.Minute
		if times[minute] {
			return false
		}
		times[minute] = true
	}
	return true
}

// RoutineDefinitionsEqual compares normalized drafts, including their defaults.
func RoutineDefinitionsEqual(a, b json.RawMessage) bool {
	_, aa, err := ParseRoutineDefinition(a)
	if err != nil {
		return false
	}
	_, bb, err := ParseRoutineDefinition(b)
	return err == nil && bytes.Equal(aa, bb)
}

func routineTreeBounded(value any) bool {
	nodes := 0
	var bounded func(any, int) bool
	bounded = func(item any, depth int) bool {
		nodes++
		if nodes > 20000 || depth > 32 {
			return false
		}
		switch item := item.(type) {
		case map[string]any:
			for key, child := range item {
				if key == "__proto__" || key == "prototype" || key == "constructor" || !bounded(child, depth+1) {
					return false
				}
			}
		case []any:
			for _, child := range item {
				if !bounded(child, depth+1) {
					return false
				}
			}
		}
		return true
	}
	return bounded(value, 0)
}

// ValidateRoutineEnvelope bounds trigger and definition together, before a run
// and dispatch intent can commit. Structural steps are validated separately.
func ValidateRoutineEnvelope(raw json.RawMessage) error {
	if len(raw) > 1<<20 {
		return ErrInvalid
	}
	var value any
	if Decode(raw, &value) != nil || !routineTreeBounded(value) {
		return ErrInvalid
	}
	return nil
}
