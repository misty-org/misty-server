package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	. "github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
	"github.com/misty-org/misty-server/test/testkit"
)

// TestClosedAccountPurgeLeavesOnlyAttribution closes a populated account,
// runs every step of its deletion and checks Privacy §10: the account's own
// data is gone, a Space other people use is handed over, and only the
// attribution on content they keep still names the deleted user.
func TestClosedAccountPurgeLeavesOnlyAttribution(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	alice, err := database.CreateUser("purge-alice@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := database.CreateUser("purge-bob@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	personal := personalSpace(t, database, ctx, alice.ID)
	photo := createTestImage(t, database, alice.ID, personal.ID, "photo.jpg", "a")

	// Alice owns Studio, which Bob uses; she is a member of Bob's Club.
	studio := createTestSpace(t, database, ctx, alice.ID, "Studio")
	join(t, database, ctx, alice.ID, studio.ID, bob)
	club := createTestSpace(t, database, ctx, bob.ID, "Club")
	join(t, database, ctx, bob.ID, club.ID, alice)
	kept, _, err := database.TestingCreateSpaceMessage(ctx, alice.ID, club.ID, []MessageSpan{{Type: "text", Text: "kept for Bob"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	exec(t, database, `INSERT INTO space_notes(id,space_id,creator_user_id,visibility) VALUES('note-1',$1,$2,'private')`, personal.ID, alice.ID)
	exec(t, database, `INSERT INTO space_drawings(id,space_id,creator_user_id,visibility) VALUES('drawing-1',$1,$2,'private')`, personal.ID, alice.ID)
	exec(t, database, `INSERT INTO voice_transcripts(id,owner_user_id,source,turns) VALUES($1,$2,'{}','[]')`, uuid.New(), alice.ID)
	exec(t, database, `INSERT INTO space_agents(space_id,agent_id,owner_user_id,agent_name) VALUES($1,'agent-1',$2,'Writer')`, club.ID, alice.ID)

	before := map[string][]any{
		`SELECT count(*) FROM library_blobs WHERE security_domain_id=$1`: {personal.SecurityDomainID},
	}
	for query, args := range before {
		if n := count(t, database, query, args...); n == 0 {
			t.Fatalf("fixture missing: %s", query)
		}
	}

	// Closed, and not yet due; then with its date already here. The server
	// sends no email, so nothing else is waited for.
	if _, err := database.CloseAccount(ctx, alice.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if due, _ := database.DueClosedAccounts(ctx, 10); len(due) != 0 {
		t.Fatalf("deleted before its date: %v", due)
	}
	exec(t, database, `UPDATE account_controls SET delete_at=now()-interval '1 minute' WHERE user_id=$1`, alice.ID)
	if due, err := database.DueClosedAccounts(ctx, 10); err != nil || len(due) != 1 || due[0] != alice.ID {
		t.Fatalf("due = %v %v", due, err)
	}
	requestID := "deletion_" + uuid.NewString()
	if started, err := database.BeginClosedAccountDeletion(ctx, alice.ID, requestID, security.HashToken(requestID)); err != nil || !started {
		t.Fatalf("begin = %v %v", started, err)
	}
	if err := database.ScheduleAccountDeletion(ctx, requestID, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteAccountDeletion(ctx, requestID); err != nil {
		t.Fatal(err)
	}
	purgeSpaces(t, database, ctx, personal.ID)

	// Studio is Bob's now; the personal Space and its Library are gone.
	if owner := text(t, database, `SELECT owner_user_id FROM spaces WHERE id=$1`, studio.ID); owner != bob.ID {
		t.Fatalf("Studio's owner = %v", owner)
	}
	gone := map[string][]any{
		`SELECT count(*) FROM spaces WHERE id=$1`:                        {personal.ID},
		`SELECT count(*) FROM space_library_items WHERE id=$1`:           {photo.ID},
		`SELECT count(*) FROM library_blobs WHERE security_domain_id=$1`: {personal.SecurityDomainID},
		`SELECT count(*) FROM security_domains WHERE id=$1`:              {personal.SecurityDomainID},
		`SELECT count(*) FROM space_members WHERE user_id=$1`:            {alice.ID},
		`SELECT count(*) FROM space_notes WHERE id=$1`:                   {"note-1"},
		`SELECT count(*) FROM space_drawings WHERE id=$1`:                {"drawing-1"},
	}
	for query, args := range gone {
		if n := count(t, database, query, args...); n != 0 {
			t.Fatalf("%s = %v, want 0", query, n)
		}
	}
	if sender := text(t, database, `SELECT sender_user_id FROM space_messages WHERE id=$1`, kept.ID); sender != alice.ID {
		t.Fatalf("Bob's Club lost Alice's message: sender = %v", sender)
	}
	var email, state string
	if err := database.Conn.QueryRow(`SELECT email,lifecycle_state FROM users WHERE id=$1`, alice.ID).Scan(&email, &state); err != nil || state != "deleted" || email == alice.Email {
		t.Fatalf("users row = %q %q %v", email, state, err)
	}

	// Every column that references users is empty for Alice, unless it is
	// attribution the purge keeps on purpose.
	rows, err := database.Conn.Query(`SELECT c.conrelid::regclass::text, a.attname FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=ANY(c.conkey)
		WHERE c.contype='f' AND c.confrelid='users'::regclass ORDER BY 1,2`)
	if err != nil {
		t.Fatal(err)
	}
	type column struct{ table, name string }
	columns := []column{}
	for rows.Next() {
		var item column
		if err := rows.Scan(&item.table, &item.name); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, item)
	}
	rows.Close()
	if len(columns) < 50 {
		t.Fatalf("only %d columns reference users", len(columns))
	}
	stale := AccountPurgeKept()
	for _, item := range columns {
		name := item.table + "." + item.name
		left := count(t, database, `SELECT count(*) FROM `+item.table+` WHERE `+item.name+`=$1`, alice.ID)
		if _, allowed := AccountPurgeKept()[name]; left != 0 && !allowed {
			t.Errorf("%s still has %d rows for the deleted account", name, left)
		}
		delete(stale, name)
	}
	for name := range stale {
		t.Errorf("AccountPurgeKept names %s, which no longer references users", name)
	}
	if count(t, database, `SELECT count(*) FROM users WHERE lower(email)='purge-alice@example.test'`) != 0 {
		t.Fatal("the deleted account still holds its email")
	}
	if _, _, err := database.CreateOperatorAccount(ctx, "purge-alice@example.test", AccountLimits{}); err != nil {
		t.Fatalf("the email is free again after the purge: %v", err)
	}
}

// TestOperatorCreateRefusesAnEmailBeingDeleted answers 409, not 500.
func TestOperatorCreateRefusesAnEmailBeingDeleted(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("leaving@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.BeginAccountDeletion(ctx, user.ID, "deletion_x", security.HashToken("x"), 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.CreateOperatorAccount(ctx, "Leaving@example.test", AccountLimits{}); err != ErrAccountPendingDeletion {
		t.Fatalf("create = %v, want ErrAccountPendingDeletion", err)
	}
}

func join(t *testing.T, database *Database, ctx context.Context, ownerID, spaceID string, member *User) {
	t.Helper()
	invite, err := database.TestingInviteToSpace(ctx, ownerID, spaceID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
}

// purgeSpaces runs the Space purge, confirming each room purge as the
// collaboration service would.
func purgeSpaces(t *testing.T, database *Database, ctx context.Context, personalID string) {
	t.Helper()
	ids, err := database.DueSpacePurges(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if waiting, err := database.BeginSpacePurge(ctx, id); err != nil {
			t.Fatal(err)
		} else if id == personalID && waiting != 2 {
			t.Fatalf("the personal Space's note and drawing rooms = %d, want 2 waiting", waiting)
		}
		exec(t, database, `UPDATE space_note_control_outbox SET delivered_at=now() WHERE command='purge'`)
		exec(t, database, `UPDATE space_drawing_control_outbox SET delivered_at=now() WHERE command='purge'`)
		if waiting, err := database.BeginSpacePurge(ctx, id); err != nil || waiting != 0 {
			t.Fatalf("waiting rooms = %d %v", waiting, err)
		}
		if keys, err := database.SpacePurgeObjectKeys(ctx, id); err != nil {
			t.Fatal(err)
		} else if id == personalID && len(keys) == 0 {
			t.Fatal("the personal Space's Library objects were not listed for deletion")
		}
		if err := database.DeleteSpaceData(ctx, id); err != nil {
			t.Fatalf("delete Space %s: %v", id, err)
		}
	}
}

func exec(t *testing.T, database *Database, query string, args ...any) {
	t.Helper()
	if _, err := database.Conn.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func count(t *testing.T, database *Database, query string, args ...any) int64 {
	t.Helper()
	var value int64
	if err := database.Conn.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return value
}

func text(t *testing.T, database *Database, query string, args ...any) string {
	t.Helper()
	var value string
	if err := database.Conn.QueryRow(query, args...).Scan(&value); err != nil && err != sql.ErrNoRows {
		t.Fatalf("%s: %v", query, err)
	}
	return value
}
