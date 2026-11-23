package db

import (
	"context"
	. "github.com/misty-org/misty-server/internal/platform/db"
	"testing"
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

func TestPurgeWaitsForRoomPurgeDelivery(t *testing.T) {
	fixture := newNoteFixture(t, "note-life-delivery")
	if err := fixture.database.DeleteSpaceNote(fixture.ctx, fixture.creator, fixture.note.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.database.PurgeExpiredNotes(fixture.ctx, 100); err != nil {
		t.Fatal(err)
	}
	if state := noteLifecycleState(t, fixture, fixture.note.ID); state != NoteLifecycleDeleting {
		t.Fatalf("lifecycle = %q, want the note held until its room purge lands", state)
	}

	commands, err := fixture.database.PendingNoteControlCommands(fixture.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	var purgeCommandID string
	for _, command := range commands {
		if command.NoteID == fixture.note.ID && command.Command == "purge" {
			purgeCommandID = command.ID
		}
	}
	if purgeCommandID == "" {
		t.Fatal("no purge command was queued for the deleted note")
	}
	if err := fixture.database.MarkNoteControlDelivered(fixture.ctx, purgeCommandID); err != nil {
		t.Fatal(err)
	}

	purged, err := fixture.database.PurgeExpiredNotes(fixture.ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if purged != 1 {
		t.Fatalf("purged = %d, want the note removed once its room purge was delivered", purged)
	}
}

func TestPendingControlCommandsAreClaimedOnce(t *testing.T) {
	fixture := newNoteFixture(t, "note-life-claim")
	if err := fixture.database.DeleteSpaceNote(fixture.ctx, fixture.creator, fixture.note.ID); err != nil {
		t.Fatal(err)
	}

	first, err := fixture.database.PendingNoteControlCommands(fixture.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 {
		t.Fatal("no commands were claimed")
	}
	second, err := fixture.database.PendingNoteControlCommands(fixture.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range second {
		for _, claimed := range first {
			if command.ID == claimed.ID {
				t.Fatalf("command %s was claimed twice in a row", command.ID)
			}
		}
	}
}

func TestNoteControlBacklogCountsOnlyDueUndelivered(t *testing.T) {
	fixture := newNoteFixture(t, "note-life-backlog")
	ctx := context.Background()

	before, err := fixture.database.NoteControlBacklog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.DeleteSpaceNote(fixture.ctx, fixture.creator, fixture.note.ID); err != nil {
		t.Fatal(err)
	}
	after, err := fixture.database.NoteControlBacklog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after <= before {
		t.Fatalf("backlog = %d, want it to grow past %d after queuing a purge", after, before)
	}
}
