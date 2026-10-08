package transport

import (
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
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
				if d.ID == "app.appearance.accent" {
					valid = accentColorPattern.MatchString(v)
				}
				if d.ID == "app.appearance.theme_light_start" || d.ID == "app.appearance.theme_dark_start" {
					valid = themeTimePattern.MatchString(v)
				}
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

// accentColorPattern accepts an empty value (no accent) or a lowercase #rrggbb color.
var accentColorPattern = regexp.MustCompile(`^(#[0-9a-f]{6})?$`)

// themeTimePattern is a local 24-hour "HH:MM" for the scheduled theme.
var themeTimePattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func validStructuredPreference(id, raw string) bool {
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return false
	}
	entries, ok := value.([]any)
	if !ok {
		return false
	}
	// Tab orders and hidden navigation destinations are short lists of slugs.
	if strings.HasPrefix(id, "collections.tabs.") || id == "app.navigation.hidden" || id == "browser.toolbarHidden" {
		if len(entries) > 40 {
			return false
		}
		seen := map[string]bool{}
		for _, entry := range entries {
			tab, ok := entry.(string)
			if !ok || len(tab) == 0 || len(tab) > 64 || seen[tab] || tab[0] < 'a' || tab[0] > 'z' {
				return false
			}
			for _, char := range tab {
				if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
					return false
				}
			}
			seen[tab] = true
		}
		return true
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
		case "browser.siteStyles":
			host, _ := item["host"].(string)
			css, cssOK := item["css"].(string)
			_, darkOK := item["dark"].(bool)
			if host == "" || len(host) > 253 || !cssOK || len(css) > 32768 || !darkOK {
				return false
			}
		case "browser.contentBlockingAllowedSites":
			host, _ := item["host"].(string)
			if host == "" || len(host) > 253 {
				return false
			}
		case "browser.siteZoom":
			host, _ := item["host"].(string)
			factor, ok := item["factor"].(float64)
			if host == "" || len(host) > 253 || !ok || math.IsNaN(factor) || factor < 0.25 || factor > 5 {
				return false
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
			// Tabs may share navigation's edge; they then sit inside it.
			if !positions[nav] || !positions[tabs] {
				return false
			}
		}
	}
	return true
}
