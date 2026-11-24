package db

import (
	"testing"

	. "github.com/misty-org/misty-server/internal/platform/db"
)

func noteLifecycleState(t *testing.T, fixture noteFixture, noteID string) string {
	t.Helper()
	var state string
	if err := fixture.database.Conn.QueryRow(
		`SELECT lifecycle_state FROM space_notes WHERE id=$1`, noteID).Scan(&state); err != nil {
		t.Fatalf("reading lifecycle for %s: %v", noteID, err)
	}
	return state
}

func TestCreatorLeavingKeepsTheNoteForEveryoneElse(t *testing.T) {
	fixture := newNoteFixture(t, "note-life-leave")

	if err := fixture.database.LeaveSpace(fixture.ctx, fixture.creator, fixture.spaceID); err != nil {
		t.Fatal(err)
	}

	if state := noteLifecycleState(t, fixture, fixture.note.ID); state != NoteLifecycleActive {
		t.Fatalf("lifecycle = %q, want the note to stay active", state)
	}
	access, err := fixture.database.NoteAccessFor(fixture.ctx, fixture.member, fixture.note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !access.CanEdit {
		t.Fatalf("remaining member lost access when the creator left: %#v", access)
	}
	gone, err := fixture.database.NoteAccessFor(fixture.ctx, fixture.creator, fixture.note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gone.CanView {
		t.Fatalf("departed creator retained access: %#v", gone)
	}
}

func TestCreatorRemovedKeepsTheNote(t *testing.T) {
	fixture := newNoteFixture(t, "note-life-removed")

	if err := fixture.database.RemoveSpaceMember(fixture.ctx, fixture.owner, fixture.spaceID, fixture.creator); err != nil {
		t.Fatal(err)
	}

	if state := noteLifecycleState(t, fixture, fixture.note.ID); state != NoteLifecycleActive {
		t.Fatalf("lifecycle = %q, want the note to stay active", state)
	}
}

func TestDeletedNoteIsPurgedWithoutWaitingForARoom(t *testing.T) {
	fixture := newNoteFixture(t, "note-life-delivery")
	if err := fixture.database.DeleteSpaceNote(fixture.ctx, fixture.creator, fixture.note.ID); err != nil {
		t.Fatal(err)
	}
	purged, err := fixture.database.PurgeExpiredNotes(fixture.ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if purged != 1 {
		t.Fatalf("purged = %d, want the deleted note removed", purged)
	}
}
