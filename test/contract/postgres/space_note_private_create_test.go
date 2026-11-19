package db

import (
	"context"
	"errors"
	"testing"

	. "github.com/misty-org/misty-server/internal/platform/db"
)

// A note created private (a person's Lab questions) is its creator's alone from its first moment:
// no other member lists it, reads it or hears that it was made.
func TestNoteCreatedPrivateNeverReachesAnotherMember(t *testing.T) {
	f := newSpaceAgentFixture(t)
	ctx := context.Background()
	note, err := f.database.CreateSpaceNoteWithVisibility(ctx, f.alice.ID, f.space.ID, "Questions: Photosynthesis",
		SpaceResourceAudience{Kind: SpaceAudienceSpace}, "", VisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if note.Visibility != VisibilityPrivate {
		t.Fatalf("a note created private is %q", note.Visibility)
	}
	notes, err := f.database.AccessibleSpaceNotes(ctx, f.bob.ID, f.space.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range notes {
		if listed.ID == note.ID {
			t.Fatal("another member listed a note created private")
		}
	}
	if _, err := f.database.SpaceNoteByID(ctx, f.bob.ID, note.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("another member read a note created private: %v", err)
	}
	if got := noteEventTypesFor(t, f.database, ctx, f.bob.ID, note.ID); len(got) != 0 {
		t.Fatalf("another member received %v for a note created private", got)
	}
	if got := noteEventTypesFor(t, f.database, ctx, f.alice.ID, note.ID); len(got) == 0 {
		t.Fatal("its creator heard nothing of the note")
	}
	if _, err := f.database.CreateSpaceNoteWithVisibility(ctx, f.alice.ID, f.space.ID, "Odd", SpaceResourceAudience{Kind: SpaceAudienceSpace}, "", "secret"); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("an unknown visibility was accepted: %v", err)
	}
}
