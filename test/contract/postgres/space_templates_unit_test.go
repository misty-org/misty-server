package db

import (
	"reflect"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestBuiltInSpaceTemplatesHaveStableBetaContracts(t *testing.T) {
	templates := BuiltInSpaceTemplates()
	gotIDs := make([]string, 0, len(templates))
	for _, template := range templates {
		gotIDs = append(gotIDs, template.ID)
		if template.Version != 1 {
			t.Fatalf("template %q version = %d, want 1", template.ID, template.Version)
		}
		if template.RecommendedIntegrations == nil {
			t.Fatalf("template %q recommended integrations encoded as null", template.ID)
		}
	}
	wantIDs := []string{
		"blank",
		"student-project",
		"startup",
		"research",
		"game-development",
		"creative-team",
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("template IDs = %#v, want %#v", gotIDs, wantIDs)
	}
	if templates[0].SeedSummary.TaskCount != 0 ||
		templates[0].SeedSummary.NoteCount != 0 ||
		templates[0].SeedSummary.CollectionCount != 0 {
		t.Fatalf("Blank seed summary = %#v, want empty", templates[0].SeedSummary)
	}
	for _, template := range templates[1:] {
		if template.SeedSummary.TaskCount != 3 || template.SeedSummary.NoteCount != 1 {
			t.Fatalf("template %q seed summary = %#v", template.ID, template.SeedSummary)
		}
	}
}

func TestNormalizeSetupProvidersRejectsUnknownAndDeduplicates(t *testing.T) {
	got, err := TestingNormalizeSetupProviders([]string{"notion", "discord", "NOTION", "google"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"discord", "google", "notion"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("providers = %#v, want %#v", got, want)
	}
	if _, err := TestingNormalizeSetupProviders([]string{"slack"}); err == nil {
		t.Fatal("unknown beta provider was accepted")
	}
}

func TestBuiltInSpaceTemplateStarterContent(t *testing.T) {
	tests := []struct {
		id          string
		tasks       []string
		noteTitle   string
		collections []string
	}{
		{"blank", nil, "", nil},
		{"student-project", []string{"Agree on the goal", "Divide responsibilities", "Set the first deadline"}, "Project brief", []string{"Research", "Drafts", "Final Deliverables"}},
		{"startup", []string{"Define this week's outcome", "Talk to a first user", "Assign owners"}, "Company snapshot", []string{"Product", "Customer Research", "Brand & Pitch"}},
		{"research", []string{"Write the research question", "Collect key sources", "Set the next checkpoint"}, "Research plan", []string{"Papers", "Data", "Outputs"}},
		{"game-development", []string{"Define a playable milestone", "Assign core roles", "Schedule a playtest"}, "Game brief", []string{"Art", "Audio", "Builds & References"}},
		{"creative-team", []string{"Agree on the brief", "Assign initial deliverables", "Set a review date"}, "Creative brief", []string{"Briefs", "Inspiration", "Work in Progress", "Final"}},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			template, ok := TestingTemplateByID(test.id)
			if !ok {
				t.Fatalf("template %q is missing", test.id)
			}
			if !reflect.DeepEqual(template.Tasks, test.tasks) {
				t.Fatalf("tasks = %#v, want %#v", template.Tasks, test.tasks)
			}
			if template.NoteTitle != test.noteTitle {
				t.Fatalf("note title = %q, want %q", template.NoteTitle, test.noteTitle)
			}
			if !reflect.DeepEqual(template.Collections, test.collections) {
				t.Fatalf("collections = %#v, want %#v", template.Collections, test.collections)
			}
			if template.NoteTitle != "" && template.NoteMarkdown == "" {
				t.Fatal("planning note has no seed content")
			}
		})
	}
}
