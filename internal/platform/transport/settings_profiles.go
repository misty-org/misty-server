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
	ID        string   `json:"id"`
	Owner     string   `json:"owner"`
	Type      string   `json:"type"`
	Enum      []string `json:"enum"`
	Min       *float64 `json:"min"`
	Max       *float64 `json:"max"`
	MaxLength int      `json:"maxLength"`
	Format    string   `json:"format"`
}

var portableDefinitions = func() map[string]PreferenceDefinition {
	raw, _ := settingsDefinitions.ReadFile("settings_definitions.json")
	var definitions []PreferenceDefinition
	if err := json.Unmarshal(raw, &definitions); err != nil {
		panic(err)
	}
	result := map[string]PreferenceDefinition{}
	for _, d := range definitions {
		if d.Owner == "profile" || d.Owner == "account" {
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
			maxLength := d.MaxLength
			if maxLength == 0 {
				maxLength = 4096
			}
			if v, ok := value.(string); ok && len(v) <= maxLength {
				valid = len(d.Enum) == 0
				if d.Format == "json" {
					valid = validStructuredPreference(d.ID, v)
				}
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

func validStructuredPreference(id, raw string) bool {
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return false
	}
	if id == "files.openWith" {
		entries, ok := value.(map[string]any)
		if !ok {
			return false
		}
		for _, path := range entries {
			if text, ok := path.(string); !ok || len(text) > 4096 {
				return false
			}
		}
		return true
	}
	entries, ok := value.([]any)
	if !ok {
		return false
	}
	for _, entry := range entries {
		item, ok := entry.(map[string]any)
		if !ok {
			return false
		}
		switch id {
		case "app.shortcuts.bindings":
			if _, ok := item["commandId"].(string); !ok {
				return false
			}
			for _, slot := range []string{"primary", "alternate"} {
				if v := item[slot]; v != nil {
					if _, ok := v.(string); !ok {
						return false
					}
				}
			}
		case "app.layout.presets":
			if _, ok := item["id"].(string); !ok {
				return false
			}
			if _, ok := item["name"].(string); !ok {
				return false
			}
			positions := map[string]bool{"left": true, "right": true, "top": true, "bottom": true}
			nav, _ := item["navigation"].(string)
			tabs, _ := item["tabs"].(string)
			if !positions[nav] || !positions[tabs] || nav == tabs {
				return false
			}
		}
	}
	return true
}
