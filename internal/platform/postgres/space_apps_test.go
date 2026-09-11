package db

import "testing"

func TestCuratedSpaceTemplateApps(t *testing.T) {
	templates := BuiltInSpaceTemplates()
	expected := map[string][]string{"blank": {}, "family": {"chat", "planner", "journal", "library"}, "startup": {"chat", "inbox", "journal", "planner", "library"}, "game-development": {"chat", "journal", "planner", "library", "files", "code", "terminal"}}
	for _, template := range templates {
		ids, ok := expected[template.ID]
		if !ok {
			continue
		}
		if len(ids) != len(template.AppIDs) {
			t.Fatalf("%s apps: %v", template.ID, template.AppIDs)
		}
		for i, id := range ids {
			if template.AppIDs[i] != id {
				t.Fatalf("%s order: %v", template.ID, template.AppIDs)
			}
		}
		delete(expected, template.ID)
	}
	if len(expected) != 0 {
		t.Fatalf("missing templates: %v", expected)
	}
}
