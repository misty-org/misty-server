package db

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/test/testkit"
)

// A closing account is read-only: it keeps its sessions and reads, while every
// write path refuses it, and resuming makes it writable again.
func TestClosingAccountIsReadOnlyUntilResumed(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("closing.reader@example.com", "a-long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CreateSession("closing-session-hash", user.ID); err != nil {
		t.Fatal(err)
	}
	want := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	held, err := database.CloseAccount(ctx, user.ID, want)
	if err != nil || !held.Equal(want) {
		t.Fatalf("close = %v %v", held, err)
	}
	// A retried close keeps the first date.
	if again, err := database.CloseAccount(ctx, user.ID, want.Add(-20*24*time.Hour)); err != nil || !again.Equal(want) {
		t.Fatalf("second close = %v %v", again, err)
	}

	// Not suspended: the session stays and sign-in keeps working.
	if suspended, err := database.AccountSuspended(ctx, user.ID); err != nil || suspended {
		t.Fatalf("suspended after close = %v %v", suspended, err)
	}
	var sessions int
	if err := database.Conn.QueryRow(`SELECT count(*) FROM sessions WHERE user_id=$1`, user.ID).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("sessions after close = %d %v", sessions, err)
	}
	if suspended, err := database.SuspendedAccounts(ctx); err != nil || len(suspended) != 0 {
		t.Fatalf("suspended list = %v %v", suspended, err)
	}
	if closing, err := database.ClosingAccounts(ctx); err != nil || !closing[user.ID].Equal(want) {
		t.Fatalf("closing list = %v %v", closing, err)
	}
	// Reads still work.
	if _, err := database.AccountPortableExport(ctx, user.ID); err != nil {
		t.Fatalf("export while closing: %v", err)
	}

	// Writes are refused, naming the date.
	var closingErr AccountClosingError
	if err := database.RequireAccountWritable(ctx, user.ID); !errors.As(err, &closingErr) || !closingErr.DeleteAt.Equal(want) || !errors.Is(err, ErrAccountClosing) {
		t.Fatalf("writable while closing = %v", err)
	}
	if err := database.ResumeAccount(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.RequireAccountWritable(ctx, user.ID); err != nil {
		t.Fatalf("writable after resume = %v", err)
	}
	if closing, err := database.ClosingAccounts(ctx); err != nil || len(closing) != 0 {
		t.Fatalf("closing list after resume = %v %v", closing, err)
	}
}

// Suspension stays separate: a suspended account that is also closing is in
// both lists, and the guard refuses it outright.
func TestSuspendedAndClosingAccountStaysSuspended(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("unpaid.closing@example.com", "a-long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SuspendAccount(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CloseAccount(ctx, user.ID, time.Now().Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if suspended, err := database.AccountSuspended(ctx, user.ID); err != nil || !suspended {
		t.Fatalf("suspended after close = %v %v", suspended, err)
	}
	if closing, err := database.ClosingAccounts(ctx); err != nil || len(closing) != 1 {
		t.Fatalf("closing list = %v %v", closing, err)
	}
}
