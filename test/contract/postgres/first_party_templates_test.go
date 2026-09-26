package db

import (
	"testing"
)

func TestPersonalSpaceTemplateDoesNotCopyPersonalApps(t *testing.T) {
	database := openTestDatabase(t)
	ctx := t.Context()
	owner, err := database.CreateUser("Template owner", "personal-template-owner@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Family")
	template, err := database.SavePersonalSpaceTemplate(ctx, owner.ID, "", space.ID, "Family tools", "Built-in tools")
	if err != nil {
		t.Fatal(err)
	}
	if len(template.Apps) != 0 {
		t.Fatal("personal installs copied to collaborative template")
	}
	saved, err := database.PersonalSpaceTemplates(ctx, owner.ID)
	if err != nil || len(saved) != 1 || len(saved[0].Apps) != 0 {
		t.Fatalf("template snapshot: %v %v", saved, err)
	}
}
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
