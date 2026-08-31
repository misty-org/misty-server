package unit

import (
	"reflect"
	"slices"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestNaturalLanguageWritesAreRequiredEffects(t *testing.T) {
	prompt := `Create a notes in journal, name it "The First Operating System", and write a 5 paragraph essay on the first operating system created for computers.`
	if got := api.TestingRequiredAgentMutationTools(prompt); !reflect.DeepEqual(got, []string{"notes.create"}) {
		t.Fatalf("required natural-language effects = %v, want notes.create", got)
	}
	if got := api.TestingRequiredAgentMutationTools("How do I create a note?"); len(got) != 0 {
		t.Fatalf("capability question required writes = %v, want none", got)
	}
	multiPart := "Create a task called Review draft, write a Journal note called Draft, and schedule a calendar event for tomorrow"
	want := []string{"tasks.create", "notes.create", "calendar.create"}
	if got := api.TestingRequiredAgentMutationTools(multiPart); !reflect.DeepEqual(got, want) {
		t.Fatalf("required multi-part effects = %v, want %v", got, want)
	}
}

func TestCompileAgentIntentCarriesJournalWriteThroughClarification(t *testing.T) {
	got := api.TestingCompileAgentIntentWithContinuation(
		"The First Operating System, with five paragraphs",
		"Create a Journal note and write an essay in it",
		"What should the note be called, and how long should the essay be?",
	)
	if !slices.Contains(got, "notes.create") {
		t.Fatalf("Journal clarification capabilities = %v, want notes.create", got)
	}
}
