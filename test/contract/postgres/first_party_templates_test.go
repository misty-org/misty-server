package db

import (
	"reflect"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestFirstPartyTemplateCreationIsIdempotent(t *testing.T) {
	database := openTestDatabase(t)
	ctx := t.Context()
	owner, err := database.CreateUser("Template owner", "personal-create-template@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	first, err := database.CreateSpaceWithTemplateIdempotent(ctx, owner.ID, "Family", "family", nil, "same-request")
	if err != nil {
		t.Fatal(err)
	}
	next, err := database.CreateSpaceWithTemplateIdempotent(ctx, owner.ID, "Family", "family", nil, "same-request")
	if err != nil || first.Space.ID != next.Space.ID {
		t.Fatal("non-idempotent create", err)
	}
	notes, err := database.AccessibleSpaceNotes(ctx, owner.ID, first.Space.ID)
	if err != nil || len(notes) != 1 {
		t.Fatalf("built-in seeds: %v %v", notes, err)
	}
}

func TestPersonalSpaceTemplateCapturesStructureNotContent(t *testing.T) {
	database := openTestDatabase(t)
	ctx := t.Context()
	owner, err := database.CreateUser("Template owner", "personal-template-owner@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	source, err := database.CreateSpaceWithTemplateIdempotent(ctx, owner.ID, "Launch room", "startup", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	template, err := database.SavePersonalSpaceTemplate(ctx, owner.ID, "", source.Space.ID, "Studio launch", "Our launch setup", AllTemplateParts)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Define this week's outcome", "Talk to a first user", "Assign owners"}
	if !reflect.DeepEqual(template.SeedPreview.Tasks, want) || !template.Personal {
		t.Fatalf("captured template = %#v", template)
	}
	if !reflect.DeepEqual(template.SeedPreview.Collections, []string{"Product", "Customer Research", "Brand & Pitch"}) {
		t.Fatalf("captured collections = %#v", template.SeedPreview.Collections)
	}
	tasksOnly, err := database.SavePersonalSpaceTemplate(ctx, owner.ID, "", source.Space.ID, "Tasks only", "", TemplateParts{Tasks: true})
	if err != nil || len(tasksOnly.SeedPreview.Collections) != 0 || len(tasksOnly.SeedPreview.Notes) != 0 {
		t.Fatalf("tasks-only template = %#v, %v", tasksOnly, err)
	}

	created, err := database.CreateSpaceWithTemplateIdempotent(ctx, owner.ID, "Next launch", template.ID, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	copied, err := database.CreateSpaceFromSource(ctx, owner.ID, "Copy", "", source.Space.ID, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, spaceID := range []string{created.Space.ID, copied.Space.ID} {
		var tasks, albums int
		if err := database.Conn.QueryRowContext(ctx, `SELECT
			(SELECT COUNT(*) FROM space_tasks WHERE space_id=$1),
			(SELECT COUNT(*) FROM space_albums WHERE space_id=$1)`, spaceID).Scan(&tasks, &albums); err != nil {
			t.Fatal(err)
		}
		if tasks != 3 || albums != 3 {
			t.Fatalf("Space %s seeded %d tasks and %d collections, want 3 and 3", spaceID, tasks, albums)
		}
	}

	stranger, err := database.CreateUser("Stranger", "template-stranger@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateSpaceFromSource(ctx, stranger.ID, "Not mine", "", source.Space.ID, nil, ""); err == nil {
		t.Fatal("a non-member copied another person's Space")
	}
}
