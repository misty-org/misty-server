package transport

import (
	"bytes"
	"os"
	"testing"
)

func TestSettingsProfilesSchemaMatchesClient(t *testing.T) {
	server, _ := settingsDefinitions.ReadFile("settings_definitions.json")
	client, err := os.ReadFile("../../../../src/features/settings/profiles/definitions.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(server, client) {
		t.Fatal("client/server settings allowlists differ")
	}
}
func TestSettingsProfilesRejectUnknownAndInvalidValues(t *testing.T) {
	for _, values := range []map[string]any{{"credentials": "secret"}, {"browser.searchEngine": "invalid"}, {"files.hidden": "true"}, {"app.appearance.panel_opacity": 2.0}} {
		if ValidateProfilePatch(values, nil) == nil {
			t.Fatalf("accepted invalid values: %v", values)
		}
	}
	if err := ValidateProfilePatch(map[string]any{"browser.searchEngine": "bing", "files.hidden": true, "browser.downloads.directory": "/tmp", "app.zoom": 1.25, "app.shortcuts.bindings": "[]"}, nil); err != nil {
		t.Fatal(err)
	}
	if ValidateProfilePatch(map[string]any{"files.hidden": true}, []string{"files.hidden"}) == nil {
		t.Fatal("accepted contradictory patch")
	}
	if ValidateProfilePatch(nil, []string{"unknown"}) == nil {
		t.Fatal("accepted reset of unknown setting")
	}
}
