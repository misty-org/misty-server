package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestRoutedSpaceTargets(t *testing.T) {
	spaces := []db.Space{
		{ID: "space_personal", Name: "Personal", IsDefault: true, OwnerUserID: "owner"},
		{ID: "space_family", Name: "Family", OwnerUserID: "owner"},
		{ID: "space_shared", Name: "Family", OwnerUserID: "friend"},
	}
	if got, err := routedSpaceTargets(spaces, "owner", "space_family", false, false); err != nil || got[0].ID != "space_family" {
		t.Fatalf("id lookup: %v %v", got, err)
	}
	if got, err := routedSpaceTargets(spaces, "owner", "personal", true, false); err != nil || len(got) != 1 || got[0].ID != "space_personal" {
		t.Fatalf("case-insensitive name lookup: %v %v", got, err)
	}
	if _, err := routedSpaceTargets(spaces, "owner", "Family", true, false); err == nil || !strings.Contains(err.Error(), "space_shared") {
		t.Fatalf("ambiguous names must list ids: %v", err)
	}
	if _, err := routedSpaceTargets(spaces, "owner", "Work", true, false); err == nil || !strings.Contains(err.Error(), "Personal") {
		t.Fatalf("unknown names must list choices: %v", err)
	}
	if got, err := routedSpaceTargets(spaces, "owner", "", true, false); err != nil || len(got) != 3 {
		t.Fatalf("reads without a space cover every Space: %v %v", got, err)
	}
	if got, err := routedSpaceTargets(spaces, "owner", "", false, true); err != nil || got[0].ID != "space_personal" {
		t.Fatalf("creates default to the personal Space: %v %v", got, err)
	}
	if _, err := routedSpaceTargets(spaces, "owner", "", false, false); err == nil {
		t.Fatal("changes without a space must be rejected")
	}
}

func TestSplitSpaceArgument(t *testing.T) {
	arguments, reference, err := splitSpaceArgument(json.RawMessage(`{"space":" Family ","title":"Book dinner"}`))
	if err != nil || reference != "Family" || string(arguments) != `{"title":"Book dinner"}` {
		t.Fatalf("split: %s %q %v", arguments, reference, err)
	}
	if _, _, err := splitSpaceArgument(json.RawMessage(`{"space":7}`)); err == nil {
		t.Fatal("non-string space accepted")
	}
}

func TestRoutedSpaceDescriptors(t *testing.T) {
	routed := map[string]agenttools.Descriptor{}
	for _, registration := range routedSpaceRegistrations(nil) {
		routed[registration.Descriptor.Name] = registration.Descriptor
	}
	if _, ok := routed["context.get"]; ok {
		t.Fatal("context.get is redundant for account runs")
	}
	for name, wantRequired := range map[string]bool{"notes.search": false, "notes.create": false, "notes.update": true, "messages.send": true} {
		descriptor, ok := routed[name]
		if !ok {
			t.Fatalf("%s is not routed", name)
		}
		if descriptor.Locality != agenttools.LocalityRouted || descriptor.RequiredPermission != "" || strings.Contains(descriptor.Description, "the current Space") {
			t.Fatalf("%s routing metadata: %+v", name, descriptor)
		}
		var schema struct {
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
		}
		if err := json.Unmarshal(descriptor.InputSchema, &schema); err != nil || schema.Properties["space"] == nil {
			t.Fatalf("%s schema lacks space: %s", name, descriptor.InputSchema)
		}
		required := false
		for _, field := range schema.Required {
			required = required || field == "space"
		}
		if required != wantRequired {
			t.Fatalf("%s space required = %v", name, required)
		}
	}
	if _, err := agenttools.New(routedSpaceRegistrations(nil)...); err != nil {
		t.Fatalf("routed registrations are invalid: %v", err)
	}
}
