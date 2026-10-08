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
		if template.Icon == "" {
			t.Fatalf("template %q has no icon", template.ID)
		}
		preview := template.SeedPreview
		if preview.Tasks == nil || preview.Notes == nil || preview.Collections == nil {
			t.Fatalf("template %q seed preview encodes null lists: %#v", template.ID, preview)
		}
		if len(preview.Tasks) != template.SeedSummary.TaskCount ||
			len(preview.Notes) != template.SeedSummary.NoteCount ||
			len(preview.Collections) != template.SeedSummary.CollectionCount {
			t.Fatalf("template %q preview %#v disagrees with summary %#v", template.ID, preview, template.SeedSummary)
		}
	}
	wantIDs := []string{
		"blank",
		"startup",
		"research",
		"student-project",
		"creative-team",
		"game-development",
		"family",
		"trip",
		"event",
		"client-work",
		"household",
		"book-club",
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("template IDs = %#v, want %#v", gotIDs, wantIDs)
	}
	if templates[0].SeedSummary != (SpaceTemplateSeedSummary{}) || templates[0].SuggestedName != "" {
		t.Fatalf("Blank = %#v, want no starter content or name", templates[0])
	}
	for _, template := range templates[1:] {
		if template.SuggestedName == "" || template.SeedSummary.NoteCount != 1 {
			t.Fatalf("template %q = %#v", template.ID, template)
		}
	}
}

func TestNormalizeSetupProvidersRejectsUnknownAndDeduplicates(t *testing.T) {
	got, err := TestingNormalizeSetupProviders([]string{"google", " GOOGLE "})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"google"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("providers = %#v, want %#v", got, want)
	}
	for _, retired := range []string{"notion", "discord", "slack"} {
		if _, err := TestingNormalizeSetupProviders([]string{retired}); err == nil {
			t.Fatalf("retired provider %q was accepted", retired)
		}
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
			tasks := template.SeedPreview.Tasks
			if len(tasks) == 0 {
				tasks = nil
			}
			if !reflect.DeepEqual(tasks, test.tasks) {
				t.Fatalf("tasks = %#v, want %#v", tasks, test.tasks)
			}
			noteTitle := ""
			if len(template.SeedPreview.Notes) > 0 {
				noteTitle = template.SeedPreview.Notes[0]
			}
			if noteTitle != test.noteTitle {
				t.Fatalf("note title = %q, want %q", noteTitle, test.noteTitle)
			}
			collections := template.SeedPreview.Collections
			if len(collections) == 0 {
				collections = nil
			}
			if !reflect.DeepEqual(collections, test.collections) {
				t.Fatalf("collections = %#v, want %#v", collections, test.collections)
			}
		})
	}
}
