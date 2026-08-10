package db

import (
	"context"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
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

// Notes belong to the Space, not to the person who typed them first, so a
// creator leaving must not take the Space's document with them.
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
	// The departed creator is no longer a member, so they lose access.
	gone, err := fixture.database.NoteAccessFor(fixture.ctx, fixture.creator, fixture.note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gone.CanView {
		t.Fatalf("departed creator retained access: %#v", gone)
	}
}

// Removal by the owner behaves the same as leaving voluntarily.
func TestCreatorRemovedKeepsTheNote(t *testing.T) {
	fixture := newNoteFixture(t, "note-life-removed")

	if err := fixture.database.RemoveSpaceMember(fixture.ctx, fixture.owner, fixture.spaceID, fixture.creator); err != nil {
		t.Fatal(err)
	}

	if state := noteLifecycleState(t, fixture, fixture.note.ID); state != NoteLifecycleActive {
		t.Fatalf("lifecycle = %q, want the note to stay active", state)
	}
}

// A departing member's own UI state goes away, since it can never apply again.
func TestLeavingClearsOnlyThatMembersPreferences(t *testing.T) {
	fixture := newNoteFixture(t, "note-life-prefs")
	for _, userID := range []string{fixture.member, fixture.owner} {
		if _, err := fixture.database.Conn.Exec(
			`INSERT INTO space_note_preferences(note_id,user_id,is_favorite) VALUES($1,$2,TRUE)`,
			fixture.note.ID, userID); err != nil {
			t.Fatal(err)
		}
	}

	if err := fixture.database.LeaveSpace(fixture.ctx, fixture.member, fixture.spaceID); err != nil {
		t.Fatal(err)
	}

	var departed, remaining int
	if err := fixture.database.Conn.QueryRow(
		`SELECT COUNT(*) FROM space_note_preferences WHERE note_id=$1 AND user_id=$2`,
		fixture.note.ID, fixture.member).Scan(&departed); err != nil {
		t.Fatal(err)
	}
	if departed != 0 {
		t.Fatalf("departed member kept %d preference rows", departed)
	}
	if err := fixture.database.Conn.QueryRow(
		`SELECT COUNT(*) FROM space_note_preferences WHERE note_id=$1 AND user_id=$2`,
		fixture.note.ID, fixture.owner).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatal("an unrelated member's preferences were cleared")
	}
}

// Deleting an account must not delete the Space's notes. Destructive control
// moves to the Space owner instead.
func TestAccountDeletionReassignsNotesToTheSpaceOwner(t *testing.T) {
	fixture := newNoteFixture(t, "note-life-account")

	if err := fixture.database.PurgeNotesForDeletedAccount(fixture.ctx, fixture.creator); err != nil {
		t.Fatal(err)
	}

	if state := noteLifecycleState(t, fixture, fixture.note.ID); state != NoteLifecycleActive {
		t.Fatalf("lifecycle = %q, want the note preserved", state)
	}
	var creatorUserID string
	if err := fixture.database.Conn.QueryRow(
		`SELECT creator_user_id FROM space_notes WHERE id=$1`, fixture.note.ID).Scan(&creatorUserID); err != nil {
		t.Fatal(err)
	}
	if creatorUserID != fixture.owner {
		t.Fatalf("creator_user_id = %q, want the Space owner %q", creatorUserID, fixture.owner)
	}
	// The note is still readable and editable by the remaining members.
	access, err := fixture.database.NoteAccessFor(fixture.ctx, fixture.member, fixture.note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !access.CanEdit {
		t.Fatalf("member lost access after the creator's account was deleted: %#v", access)
	}
}

// Deleting a note queues a room purge, and the row is held until that purge is
// confirmed delivered so the Durable Object is never stranded.
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

// Two workers must not deliver the same command twice.
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
	// The per-attempt backoff pushes the next attempt out, so an immediate
	// second poll must not hand back the same command.
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
