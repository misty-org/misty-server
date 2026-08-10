package db

import (
	"context"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type noteFixture struct {
	database *Database
	ctx      context.Context
	spaceID  string
	creator  string
	member   string
	owner    string
	note     *SpaceNote
}

// newNoteFixture builds a Space owned by `owner`, joined by `creator` and
// `member`, with one note created by `creator`.
//
// The owner is deliberately a different user from the note creator, so tests
// can tell apart the capabilities that come from Space ownership and the ones
// that come from having created the note.
func newNoteFixture(t *testing.T, prefix string) noteFixture {
	t.Helper()
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Note Owner", prefix+"-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	creator, err := database.CreateUser("Note Creator", prefix+"-creator@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Note Member", prefix+"-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Note Space")
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{creator.ID, member.ID} {
		invite, inviteErr := database.InviteToSpace(ctx, owner.ID, space.ID, userEmail(t, database, user))
		if inviteErr != nil {
			t.Fatal(inviteErr)
		}
		if _, respondErr := database.RespondToSpaceInvite(ctx, user, invite.ID, true); respondErr != nil {
			t.Fatal(respondErr)
		}
	}
	note, err := database.CreateSpaceNote(ctx, creator.ID, space.ID, "Beta launch checklist")
	if err != nil {
		t.Fatal(err)
	}
	return noteFixture{
		database: database, ctx: ctx, spaceID: space.ID,
		creator: creator.ID, member: member.ID, owner: owner.ID, note: note,
	}
}

func userEmail(t *testing.T, database *Database, userID string) string {
	t.Helper()
	var email string
	if err := database.Conn.QueryRow(`SELECT email FROM users WHERE id=$1`, userID).Scan(&email); err != nil {
		t.Fatal(err)
	}
	return email
}

// Native notes are Space documents: every current member can read and write
// them, with no per-note grant step.
func TestEverySpaceMemberCanReadAndEditANote(t *testing.T) {
	fixture := newNoteFixture(t, "note-shared")

	for name, userID := range map[string]string{
		"creator": fixture.creator, "other member": fixture.member, "space owner": fixture.owner,
	} {
		access, err := fixture.database.NoteAccessFor(fixture.ctx, userID, fixture.note.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !access.CanView || !access.CanEdit {
			t.Fatalf("%s access = %#v, want view and edit", name, access)
		}
	}
}

// Destructive control is narrower than edit access: the creator and the Space
// owner may delete, an ordinary member may not.
func TestOnlyCreatorAndOwnerCanDelete(t *testing.T) {
	fixture := newNoteFixture(t, "note-delete-rights")

	for name, userID := range map[string]string{"creator": fixture.creator, "space owner": fixture.owner} {
		access, err := fixture.database.NoteAccessFor(fixture.ctx, userID, fixture.note.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !access.CanDelete {
			t.Fatalf("%s should be able to delete: %#v", name, access)
		}
	}

	access, err := fixture.database.NoteAccessFor(fixture.ctx, fixture.member, fixture.note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if access.CanDelete {
		t.Fatalf("an ordinary member could delete a note: %#v", access)
	}
}

// Membership is the whole gate, so someone outside the Space gets nothing.
func TestNonMemberHasNoNoteAccess(t *testing.T) {
	fixture := newNoteFixture(t, "note-outsider")
	outsider, err := fixture.database.CreateUser("Outsider", "note-outsider-stranger@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}

	access, err := fixture.database.NoteAccessFor(fixture.ctx, outsider.ID, fixture.note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if access.CanView || access.CanEdit || access.CanDelete {
		t.Fatalf("non-member access = %#v, want nothing", access)
	}
}

// A former member loses access the moment their membership ends.
func TestFormerSpaceMemberLosesNoteAccess(t *testing.T) {
	fixture := newNoteFixture(t, "note-former")

	before, err := fixture.database.NoteAccessFor(fixture.ctx, fixture.member, fixture.note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !before.CanEdit {
		t.Fatal("test precondition failed: member could not edit before leaving")
	}

	if err := fixture.database.LeaveSpace(fixture.ctx, fixture.member, fixture.spaceID); err != nil {
		t.Fatal(err)
	}

	after, err := fixture.database.NoteAccessFor(fixture.ctx, fixture.member, fixture.note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.CanView || after.CanEdit {
		t.Fatalf("former member retained access: %#v", after)
	}
}

// Archived notes are inaccessible to everyone until restored, including the
// creator and the Space owner.
func TestArchivedNoteIsInaccessibleToEveryone(t *testing.T) {
	fixture := newNoteFixture(t, "note-archived")

	if _, err := fixture.database.Conn.Exec(
		`UPDATE space_notes SET lifecycle_state='archived_creator_left',archived_at=NOW(),purge_after=NOW()+INTERVAL '30 days' WHERE id=$1`,
		fixture.note.ID); err != nil {
		t.Fatal(err)
	}

	for name, userID := range map[string]string{
		"creator": fixture.creator, "member": fixture.member, "space owner": fixture.owner,
	} {
		access, err := fixture.database.NoteAccessFor(fixture.ctx, userID, fixture.note.ID)
		if err != nil {
			t.Fatal(err)
		}
		if access.CanView {
			t.Fatalf("%s could view an archived note: %#v", name, access)
		}
	}
}

// An unauthorized read must be indistinguishable from a note that does not
// exist, so a non-member cannot probe for note ids.
func TestUnauthorizedNoteLooksExactlyLikeAMissingNote(t *testing.T) {
	fixture := newNoteFixture(t, "note-indistinct")
	outsider, err := fixture.database.CreateUser("Prober", "note-indistinct-prober@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}

	unauthorized, err := fixture.database.RequireNoteView(fixture.ctx, outsider.ID, fixture.note.ID)
	if !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("unauthorized RequireNoteView() = %v, want ErrSpaceNotFound", err)
	}
	missing, err := fixture.database.RequireNoteView(fixture.ctx, outsider.ID, "note_00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("missing RequireNoteView() = %v, want ErrSpaceNotFound", err)
	}
	// Any difference between the two would itself confirm the note exists.
	if unauthorized != missing {
		t.Fatalf("unauthorized %#v differs from missing %#v", unauthorized, missing)
	}
}

// Listing returns every active note in the Space, with the caller's own
// effective role attached.
func TestAccessibleNotesListsEverySpaceNote(t *testing.T) {
	fixture := newNoteFixture(t, "note-list")
	second, err := fixture.database.CreateSpaceNote(fixture.ctx, fixture.member, fixture.spaceID, "Member's own note")
	if err != nil {
		t.Fatal(err)
	}

	notes, err := fixture.database.AccessibleSpaceNotes(fixture.ctx, fixture.member, fixture.spaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 {
		t.Fatalf("member sees %d notes, want both", len(notes))
	}
	roles := map[string]string{}
	for _, note := range notes {
		roles[note.ID] = note.Role
	}
	// The caller is the creator of one and a plain editor on the other.
	if roles[second.ID] != NoteRoleCreator {
		t.Fatalf("role on own note = %q, want creator", roles[second.ID])
	}
	if roles[fixture.note.ID] != NoteRoleEditor {
		t.Fatalf("role on another member's note = %q, want editor", roles[fixture.note.ID])
	}

	// A non-member sees nothing at all.
	outsider, err := fixture.database.CreateUser("Listing Outsider", "note-list-outsider@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.AccessibleSpaceNotes(fixture.ctx, outsider.ID, fixture.spaceID); err == nil {
		t.Fatal("a non-member listed the Space's notes")
	}
}
