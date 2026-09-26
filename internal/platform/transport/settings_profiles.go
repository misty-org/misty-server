package transport

import (
	"embed"
	"encoding/json"
	"fmt"
	"math"
)

//go:embed settings_definitions.json
var settingsDefinitions embed.FS

type PreferenceDefinition struct {
	ID    string   `json:"id"`
	Owner string   `json:"owner"`
	Type  string   `json:"type"`
	Enum  []string `json:"enum"`
	Min   *float64 `json:"min"`
	Max   *float64 `json:"max"`
}

var portableDefinitions = func() map[string]PreferenceDefinition {
	raw, _ := settingsDefinitions.ReadFile("settings_definitions.json")
	var definitions []PreferenceDefinition
	if err := json.Unmarshal(raw, &definitions); err != nil {
		panic(err)
	}
	result := map[string]PreferenceDefinition{}
	for _, d := range definitions {
		if d.Owner == "profile" {
			result[d.ID] = d
		}
	}
	return result
}()

func ValidateProfilePatch(values map[string]any, unset []string) error {
	if len(values)+len(unset) > 256 {
		return fmt.Errorf("too many preference changes")
	}
	for _, key := range unset {
		if _, ok := portableDefinitions[key]; !ok {
			return fmt.Errorf("unknown portable setting %q", key)
		}
		if _, duplicate := values[key]; duplicate {
			return fmt.Errorf("setting cannot be set and reset together")
		}
	}
	for key, value := range values {
		d, ok := portableDefinitions[key]
		if !ok {
			return fmt.Errorf("unknown portable setting %q", key)
		}
		valid := false
		switch d.Type {
		case "boolean":
			_, valid = value.(bool)
		case "string":
			if v, ok := value.(string); ok && len(v) <= 4096 {
				valid = len(d.Enum) == 0
				for _, option := range d.Enum {
					if option == v {
						valid = true
					}
				}
			}
		case "number":
			if v, ok := value.(float64); ok {
				valid = !math.IsNaN(v) && !math.IsInf(v, 0) && (d.Min == nil || v >= *d.Min) && (d.Max == nil || v <= *d.Max)
			}
		}
		if !valid {
			return fmt.Errorf("invalid value for %q", key)
		}
	}
	return nil
}
