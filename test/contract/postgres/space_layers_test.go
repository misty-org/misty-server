package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/kannachi323/misty/server/internal/accounts"
	. "github.com/kannachi323/misty/server/internal/platform/db"
)

// asMember runs fn as one person under the app role, so the RLS policies
// themselves decide what the queries see.
func asMember(t *testing.T, database *Database, userID string, fn func(*sql.Tx) error) {
	t.Helper()
	var appRole bool
	if err := database.Conn.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app')`).Scan(&appRole); err != nil {
		t.Fatal(err)
	}
	if !appRole {
		t.Skip("misty_app role is not installed")
	}
	if err := database.WithRLSContext(context.Background(), accounts.UserScope(userID), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`SET LOCAL ROLE misty_app`); err != nil {
			return err
		}
		return fn(tx)
	}); err != nil {
		t.Fatal(err)
	}
}

func countRows(t *testing.T, tx *sql.Tx, query string, args ...any) int {
	t.Helper()
	var n int
	if err := tx.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A note toggled private disappears for everyone else and their agents: they
// can't list, read, search or change it, by the queries or by RLS alone.
func TestPrivateNoteOpensOnlyForItsCreator(t *testing.T) {
	f := newMemberRequestFixture(t)
	ctx := context.Background()
	note, err := f.database.CreateSpaceNote(ctx, f.alice.ID, f.space.ID, "Salary notes")
	if err != nil {
		t.Fatal(err)
	}
	if note.Visibility != VisibilityPublic {
		t.Fatalf("a new note in a shared Space is %q", note.Visibility)
	}
	if err := f.database.SetSpaceItemVisibility(ctx, f.bob.ID, f.space.ID, "note", note.ID, VisibilityPrivate); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("someone else made the note private: %v", err)
	}
	if err := f.database.SetSpaceItemVisibility(ctx, f.alice.ID, f.space.ID, "note", note.ID, VisibilityPrivate); err != nil {
		t.Fatal(err)
	}

	notes, err := f.database.AccessibleSpaceNotes(ctx, f.bob.ID, f.space.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range notes {
		if listed.ID == note.ID {
			t.Fatal("another member listed a private note")
		}
	}
	if _, err := f.database.SpaceNoteByID(ctx, f.bob.ID, note.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("another member read a private note: %v", err)
	}
	if access, err := f.database.NoteAccessFor(ctx, f.bob.ID, note.ID); err != nil || access.CanView || access.CanEdit || access.CanDelete {
		t.Fatalf("another member's access to a private note = %#v, %v", access, err)
	}
	if err := f.database.DeleteSpaceNote(ctx, f.bob.ID, note.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("another member deleted a private note: %v", err)
	}
	if hits, err := f.database.SearchAIRetrieval(ctx, f.bob.ID, "Salary", nil, 20, f.space.ID); err != nil {
		t.Fatal(err)
	} else {
		for _, hit := range hits {
			if hit.SourceID == note.ID {
				t.Fatal("another member's search found a private note")
			}
		}
	}

	// Its creator still sees it, except on work that may read only the
	// public layer (a teammate's ask of the creator's agent).
	if _, err := f.database.SpaceNoteByID(ctx, f.alice.ID, note.ID); err != nil {
		t.Fatalf("the creator lost their private note: %v", err)
	}
	if _, err := f.database.SpaceNoteByID(WithPublicLayerOnly(ctx), f.alice.ID, note.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("public-only work read a private note: %v", err)
	}

	// RLS alone: a forgotten filter still can't leak it.
	asMember(t, f.database, f.bob.ID, func(tx *sql.Tx) error {
		if n := countRows(t, tx, `SELECT count(*) FROM space_notes WHERE id=$1`, note.ID); n != 0 {
			t.Fatalf("RLS let another member select a private note (%d rows)", n)
		}
		result, err := tx.Exec(`UPDATE space_notes SET title_projection='Mine now' WHERE id=$1`, note.ID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 0 {
			t.Fatal("RLS let another member change a private note")
		}
		return nil
	})
	asMember(t, f.database, f.alice.ID, func(tx *sql.Tx) error {
		if n := countRows(t, tx, `SELECT count(*) FROM space_notes WHERE id=$1`, note.ID); n != 1 {
			t.Fatalf("RLS hid a private note from its creator (%d rows)", n)
		}
		return nil
	})
}

// A private agent is invisible to the rest of the Space and can't be asked
// there; its owner still can.
func TestPrivateAgentCantBeMentionedBySomeoneElse(t *testing.T) {
	f := newMemberRequestFixture(t)
	ctx := context.Background()
	if err := f.database.SetSpaceItemVisibility(ctx, f.bob.ID, f.space.ID, "agent", f.bobAgent.ID, VisibilityPrivate); err != nil {
		t.Fatal(err)
	}
	agents, err := f.database.SpaceAgents(ctx, f.alice.ID, f.space.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range agents {
		if agent.AgentID == f.bobAgent.ID {
			t.Fatal("a private agent is in another member's list")
		}
	}
	if _, err := f.request("private-agent"); !errors.Is(err, ErrAgentNotInSpace) {
		t.Fatalf("asking someone else's private agent = %v", err)
	}
	if mine, err := f.database.SpaceAgents(ctx, f.bob.ID, f.space.ID); err != nil || len(mine) == 0 {
		t.Fatalf("the owner lost their private agent: %#v, %v", mine, err)
	}
	asMember(t, f.database, f.alice.ID, func(tx *sql.Tx) error {
		if n := countRows(t, tx, `SELECT count(*) FROM space_agents WHERE agent_id=$1`, f.bobAgent.ID); n != 0 {
			t.Fatalf("RLS let another member see a private agent (%d rows)", n)
		}
		return nil
	})
}

// New items start private in a Space only their owner belongs to.
func TestItemsStartPrivateInASoloSpace(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("solo-layers@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, user.ID, "Just me")
	note, err := database.CreateSpaceNote(ctx, user.ID, space.ID, "Draft")
	if err != nil {
		t.Fatal(err)
	}
	if note.Visibility != VisibilityPrivate {
		t.Fatalf("a new note in a solo Space is %q", note.Visibility)
	}
}

// Leaving a Space deletes the leaver's private items and memory there in the
// same transaction; their public items stay with the Space.
func TestLeavingASpaceDeletesThePrivateLayer(t *testing.T) {
	f := newMemberRequestFixture(t)
	ctx := context.Background()
	private, err := f.database.CreateSpaceNote(ctx, f.bob.ID, f.space.ID, "Bob's private")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.database.SetSpaceItemVisibility(ctx, f.bob.ID, f.space.ID, "note", private.ID, VisibilityPrivate); err != nil {
		t.Fatal(err)
	}
	public, err := f.database.CreateSpaceNote(ctx, f.bob.ID, f.space.ID, "Bob's public")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.database.LeaveSpace(ctx, f.bob.ID, f.space.ID); err != nil {
		t.Fatal(err)
	}
	var privateState, publicState string
	if err := f.database.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT lifecycle_state FROM space_notes WHERE id=$1`, private.ID).Scan(&privateState); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT lifecycle_state FROM space_notes WHERE id=$1`, public.ID).Scan(&publicState)
	}); err != nil {
		t.Fatal(err)
	}
	if privateState != NoteLifecycleDeleting || publicState != NoteLifecycleActive {
		t.Fatalf("after leaving: private note %q, public note %q", privateState, publicState)
	}
}
